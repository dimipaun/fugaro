//go:build docker

package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
	"gopkg.in/yaml.v3"
)

// The two images this test loads, written by scan_catalog_test.go and named
// here so that a leftover on a daemon is recognisable as this test's.
const (
	scanCatalogImageTag = "fugaro-scan-catalog:test"
	scanCatalogLeakTag  = "fugaro-scan-catalog-leak:test"
)

// TestScanSecretsOnACatalogShapedImage loads an image shaped like the one a
// `fugaro image build go` produces -- the go entry of the project image catalog
// (docs/project-image-catalog) -- into a real daemon and runs the real
// images/scan.sh --secrets against it with the Trivy digest that script pins.
//
// The hermetic tests in images/scan_test.go run the scan against a fake docker
// and a fixture tar, so they say nothing about what a daemon and a loaded image
// really hand over. On 2026-11-18 they passed while two real faults stopped a
// catalog build twice: Trivy flagged the placeholder service account in
// gocloud.dev's own source and the Go toolchain's own test key, both baked in
// by the entry's `go mod download && go build ./...` (fixed by the allowances
// in images/trivy-secret.yaml, #3201); and the layer fence refused an image
// whose config listed 20 layer IDs and 19 distinct, because BuildKit gives
// every no-op RUN the same empty layer (fixed in images/scan.sh, #3202). The
// fixture has that shape: the two allowed paths, an empty layer the image's
// history lists twice, and -- in the second image -- a private key in the
// checkout that the scan must still refuse.
//
// The fixture's layers are written by writeScanCatalogImage rather than built
// from a Dockerfile, because what a no-op step leaves is the builder's to
// shape and no Dockerfile can promise the repeat: the daemon this test was
// first read on folded two `RUN true`s into one empty layer, and another gives
// each of them the mount points it made with a fresh mtime. A loaded image is
// the daemon's to reshape all the same -- the guard below is what says it did
// not -- and everything past the load is real: a real daemon's inspect, save
// and export, and the pinned Trivy over them.
//
// It needs a Docker daemon and pulls the pinned Trivy image, so it runs only
// under the docker tag: never in the default `go test ./...`, and in CI only
// in images.yml's docker-tests job. images/README.md has how to run it.
func TestScanSecretsOnACatalogShapedImage(t *testing.T) {
	testutil.RequireDocker(t)
	files := scanFixtureFiles(t)
	image := loadScanCatalogImage(t, scanCatalogImageTag, scanCatalogLayers(t, files))

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
		t.Fatalf("the loaded image lists %d layers and no two share a diff ID: this daemon reshaped the fixture, so the shape this test pins is not built here", len(layers))
	}

	// The fixtures are at the paths the allowances name, not merely somewhere
	// in the image: a layer that landed elsewhere would make the scan's pass
	// below mean nothing.
	for path, want := range map[string]string{
		scanModuleCachePath: files["gcsblob.go"],
		scanToolchainPath:   files["platform_root_key.pem"],
	} {
		if got := scanImageFile(t, image, path); got != strings.TrimSpace(want) {
			t.Errorf("the image's %s holds %q, want the fixture", path, got)
		}
	}

	// The assertion the whole test is for: the scan clears an image whose only
	// findings are the two its own config allows, and the layer it lists twice
	// costs it nothing.
	clean, err := scanSecrets(t, "scan.sh", image, nil)
	if err != nil {
		t.Fatalf("the scan refused an image whose only findings are the two its config allows: %v\n%s", err, clean)
	}

	t.Run("the findings the allowances clear are really there", func(t *testing.T) {
		// The same script and the same pinned Trivy, with the allow-rules left
		// out of the config: the two placeholders have to be findings of their
		// own, or the pass above means nothing and an allowance could be
		// dropped without a test noticing. Removing one for real is this
		// subtest run against the config as it stands.
		out, err := scanSecrets(t, stripAllowances(t), image, nil)
		if err == nil {
			t.Fatalf("an image holding both placeholders scanned clean with no allowance in the config: Trivy's rules no longer flag them, so the allowances clear nothing\n%s", out)
		}
		for _, path := range []string{scanModuleCachePath, scanToolchainPath} {
			if !strings.Contains(out, strings.TrimPrefix(path, "/")) {
				t.Errorf("the refusal does not name %s:\n%s", path, out)
			}
		}
	})

	t.Run("a key in the checkout is still refused", func(t *testing.T) {
		leak := loadScanCatalogImage(t, scanCatalogLeakTag, scanCatalogLeakLayers(t, files))
		out, err := scanSecrets(t, "scan.sh", leak, nil)
		if err == nil {
			t.Fatalf("the scan cleared an image holding a private key at %s:\n%s", scanLeakPath, out)
		}
		if !strings.Contains(out, "key.pem") {
			t.Errorf("the refusal does not name the file the scan found: %s", out)
		}
	})

	t.Run("a layer with no decompressor is still refused", func(t *testing.T) {
		// A daemon that keeps a layer as it was pushed hands it over in a codec
		// the scan reads only through a program, and tar is that program's
		// caller: GNU tar 1.35 with no gzip on its PATH exits 2 on a gzip layer
		// rather than reading it (checked on this machine, 2026-11-18). A PATH
		// with every other program is therefore the worker that cannot read it,
		// and the fence has to refuse an image it read only in part.
		out, err := scanSecrets(t, "scan.sh", image, envWithPath(pathWithoutProgram(t, "gzip")))
		if err == nil {
			t.Skipf("this daemon's save hands over no layer this worker cannot read -- a store that keeps its layers as written and re-exports the plain diff tar of each, which is what the classic store does -- so the fence's undecodable case is not built here: images/scan_catalog_test.go's TestScanReadsTheCatalogFixture pins it over a save whose layers really are gzip")
		}
		if !strings.Contains(out, "holds a layer this cannot decode") {
			t.Fatalf("the scan refused the image over something other than an unreadable layer: %v\n%s", err, out)
		}
	})
}

// loadScanCatalogImage loads one of the fixtures the daemon's platform and
// returns its tag. The image is dropped before it is loaded and again when the
// test ends: they are small, but they are this test's, and a run of the docker
// tests should not leave tags on the daemon.
func loadScanCatalogImage(t *testing.T, tag string, layers []scanCatalogLayer) string {
	t.Helper()
	removeImages(t, tag)
	image := writeScanCatalogImage(t, layers, tag, testutil.DockerPlatform(t))
	testutil.Docker(t, "load", "--input", image.path)
	return tag
}

// removeImages drops images now and again when the test ends. The removal
// before a load is what makes a load start from no image rather than from
// whatever an earlier one left under the tag.
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

// scanImageFile reads path out of the image's own filesystem: a container is
// created from it and the file copied out of that, since the fixture is a
// filesystem and nothing else -- it has no shell to run `cat` with, and the
// scan's own leak check reads it the same way.
func scanImageFile(t *testing.T, image, path string) string {
	t.Helper()
	cid := testutil.Docker(t, "create", image)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", cid).Run() })
	out := filepath.Join(t.TempDir(), filepath.Base(path))
	testutil.Docker(t, "cp", cid+":"+path, out)
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(body))
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

// scanSecrets runs script -- images/scan.sh, or the copy stripAllowances
// writes -- with --secrets on image, from this package's directory, the way
// the images workflow and the catalog build run it, and returns its combined
// output and error. env, where given, replaces the process environment.
func scanSecrets(t *testing.T, script, image string, env []string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", script, "--secrets", image)
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// stripAllowances writes a copy of images/scan.sh beside a copy of its config
// with every allow-rule left out, and returns the script's path: the scan the
// two allowances are the difference to. The rules are carried over as they are
// parsed from the file, so the copy clears exactly what the real config clears
// minus the allowances -- Trivy adds these to its own rules either way.
func stripAllowances(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script, err := os.ReadFile("scan.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scan.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("trivy-secret.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Rules []map[string]any `yaml:"rules"`
	}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Rules) == 0 {
		t.Fatal("images/trivy-secret.yaml parsed as no rules at all")
	}
	stripped, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trivy-secret.yaml"), stripped, 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "scan.sh")
}
