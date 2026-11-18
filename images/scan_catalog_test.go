package images_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The three paths the fixture image puts files at. The first two are the paths
// the allow-rules in images/trivy-secret.yaml excuse a catalog build's own
// false positives at, written here as the image's own filesystem paths (the
// config matches Trivy's layer-relative path, which is the same path without
// the leading slash; images/scan_test.go's
// TestTrivyConfigAllowsTheGoToolchainFalsePositives holds that spelling to the
// config, this test holds a real image to it). The third is a credential no
// allowance covers, in the checkout a build bakes.
const (
	scanModuleCachePath = "/home/fugaro/go/pkg/mod/gocloud.dev@v0.46.0/blob/gcsblob/gcsblob.go"
	scanToolchainPath   = "/home/fugaro/.local/share/mise/installs/go/1.27.1/src/crypto/x509/platform_root_key.pem"
	scanLeakPath        = "/work/repo/key.pem"
)

// scanCatalogEpoch is the mtime every member of the fixture image carries: a
// fixed date, so that a layer is the same bytes wherever and whenever it is
// written (its diff ID is a hash of them), and a real one, because a daemon
// unpacks the layer onto a filesystem rather than only reading it.
var scanCatalogEpoch = time.Unix(1600000000, 0).UTC()

// scanCatalogEnv is the environment the fixture image's config sets: what a
// base image sets, with no name among them spelled like a credential (the
// scan's own check reads them, and Trivy's config scan with it).
var scanCatalogEnv = []string{
	"HOME=/home/fugaro",
	"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	"LANG=C.UTF-8",
}

// The annotations a save puts on the manifest descriptor it tags. containerd
// names the image it imports by the first, and reads its tag from the second
// as the OCI layout's own convention has it (a tag alone, the name beside it):
// a load that finds neither loads the image with no name to scan.
const (
	imageNameAnnotation = "io.containerd.image.name"
	refNameAnnotation   = "org.opencontainers.image.ref.name"
)

// The media types the fixture image is written with: the OCI layout's own, the
// pair `docker save` writes and `docker load` reads.
const (
	ociIndexMediaType    = "application/vnd.oci.image.index.v1+json"
	ociManifestMediaType = "application/vnd.oci.image.manifest.v1+json"
	ociConfigMediaType   = "application/vnd.oci.image.config.v1+json"
	ociLayerMediaType    = "application/vnd.oci.image.layer.v1.tar+gzip"
)

// scanCatalogLayer is one layer of the fixture image: what a step of the build
// left, and how many of the image's steps made it. A step that changed nothing
// leaves the empty layer every such step leaves, so the config lists that
// layer's diff ID once per step -- the shape images/scan.sh's layer count has
// to survive, and the one no Dockerfile can promise: what a build's no-op step
// leaves is the builder's to shape (the daemon this test was first read on
// folded two `RUN true`s into one empty layer, the one that reported it gives
// each of them the mount points it made, with a fresh mtime).
type scanCatalogLayer struct {
	createdBy string
	files     map[string]string
	steps     int
}

// scanCatalogImage is the fixture image as a daemon receives it: the tar
// `docker load` reads, the name it is loaded under, the layer IDs its config
// lists in order -- a repeat among them included, which is what `docker
// inspect .RootFS.Layers` reports for the image -- and the filesystem the
// image ends with.
type scanCatalogImage struct {
	path    string
	tag     string
	diffIDs []string
	files   map[string]string
}

// scanCatalogLayers is the fixture image's own steps: the two paths a catalog
// build's false positives come from, each in the layer the step that warms it
// leaves, and the empty layer two no-op steps share.
func scanCatalogLayers(t *testing.T, files map[string]string) []scanCatalogLayer {
	t.Helper()
	return []scanCatalogLayer{
		{
			createdBy: "RUN /bin/sh -c go mod download && go build ./... # buildkit",
			files:     map[string]string{strings.TrimPrefix(scanModuleCachePath, "/"): files["gcsblob.go"]},
		},
		{
			createdBy: "RUN /bin/sh -c mise install # buildkit",
			files:     map[string]string{strings.TrimPrefix(scanToolchainPath, "/"): files["platform_root_key.pem"]},
		},
		{createdBy: "RUN true", steps: 2},
	}
}

// scanCatalogLeakLayers is the same image with a checkout baked into it: the
// layer a catalog build's `COPY` of the clone leaves, holding the private key
// the scan must still refuse.
func scanCatalogLeakLayers(t *testing.T, files map[string]string) []scanCatalogLayer {
	t.Helper()
	return append([]scanCatalogLayer{{
		createdBy: "COPY / /work/repo # buildkit",
		files:     map[string]string{strings.TrimPrefix(scanLeakPath, "/"): files["key.pem"]},
	}}, scanCatalogLayers(t, files)...)
}

// writeScanCatalogImage writes the fixture image as an OCI layout tar: the
// layout `docker load` reads, in the shape `docker save` writes, with the
// manifest.json a daemon whose store is not containerd insists on beside it
// (its loader refuses an archive without one) and the blob of every layer
// written once whatever lists it twice.
//
// The layers are written here rather than built from a Dockerfile: a build
// shapes its own (see scanCatalogLayer), and this test's whole point is an
// image whose config lists one layer's diff ID twice, which only the fixture
// itself can promise. platform is the daemon's own, as <os>/<arch>, so that
// the image is the platform the daemon will unpack.
func writeScanCatalogImage(t *testing.T, layers []scanCatalogLayer, tag, platform string) scanCatalogImage {
	t.Helper()
	osName, arch, ok := strings.Cut(platform, "/")
	if !ok {
		t.Fatalf("platform %q is not <os>/<arch>", platform)
	}
	var (
		blobs       []ociBlob
		written     = map[string]bool{}
		imageLayers []ociDescriptor
		diffIDs     []string
		history     []ociHistory
		files       = map[string]string{}
	)
	// writeBlob returns the descriptor of one of the image's blobs and writes
	// it once: a content-addressed tar carries the layer two steps made once,
	// under a name every descriptor that references it agrees on.
	writeBlob := func(mediaType string, body []byte) ociDescriptor {
		sum := sha256.Sum256(body)
		desc := ociDescriptor{MediaType: mediaType, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(body))}
		if !written[desc.Digest] {
			written[desc.Digest] = true
			blobs = append(blobs, ociBlob{digest: desc.Digest, body: body})
		}
		return desc
	}
	for _, layer := range layers {
		plain, err := filesTar(layer.files)
		if err != nil {
			t.Fatal(err)
		}
		desc := writeBlob(ociLayerMediaType, gzipOf(t, plain.Bytes()))
		// The diff ID is the digest of the layer as it was written, not of the
		// codec it sits in: the config's rootfs lists the diff IDs, the
		// manifest's descriptors name the blobs, and a daemon holds the two
		// against each other when it loads the image.
		sum := sha256.Sum256(plain.Bytes())
		diffID := "sha256:" + hex.EncodeToString(sum[:])
		steps := layer.steps
		if steps == 0 {
			steps = 1
		}
		for step := 0; step < steps; step++ {
			imageLayers = append(imageLayers, desc)
			diffIDs = append(diffIDs, diffID)
			history = append(history, ociHistory{Created: scanCatalogEpoch, CreatedBy: layer.createdBy})
		}
		for name, body := range layer.files {
			files[name] = body
		}
	}
	config := ociConfig{
		Created:      scanCatalogEpoch,
		Architecture: arch,
		OS:           osName,
		Config:       ociRuntimeConfig{Cmd: []string{"/bin/sh"}, Env: scanCatalogEnv},
		RootFS:       ociRootFS{Type: "layers", DiffIDs: diffIDs},
		History:      history,
	}
	configDesc := writeBlob(ociConfigMediaType, ociJSON(t, config))
	manifestDesc := writeBlob(ociManifestMediaType, ociJSON(t, ociManifest{
		SchemaVersion: 2,
		MediaType:     ociManifestMediaType,
		Config:        configDesc,
		Layers:        imageLayers,
	}))
	// The annotations and the platform are the descriptor's, not the manifest's:
	// they are how the load learns what to call the image it just read.
	manifestDesc.Annotations = map[string]string{
		imageNameAnnotation: normalizedName(tag),
		refNameAnnotation:   tag[strings.LastIndex(tag, ":")+1:],
	}
	manifestDesc.Platform = &ociPlatform{Architecture: arch, OS: osName}
	indexJSON := ociJSON(t, ociIndex{
		SchemaVersion: 2,
		MediaType:     ociIndexMediaType,
		Manifests:     []ociDescriptor{manifestDesc},
	})

	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	add := func(name string, body []byte) {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body)), ModTime: scanCatalogEpoch,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	add("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`))
	add("index.json", indexJSON)
	for _, blob := range blobs {
		add("blobs/sha256/"+strings.TrimPrefix(blob.digest, "sha256:"), blob.body)
	}
	// The manifest a daemon whose store is not containerd loads by: its own
	// list of the image's layers, one path per diff ID and so the shared one
	// twice, beside the config file and the tag it is loaded under.
	add("manifest.json", ociJSON(t, []dockerManifestItem{{
		Config:   "blobs/sha256/" + strings.TrimPrefix(configDesc.Digest, "sha256:"),
		RepoTags: []string{tag},
		Layers:   layerBlobPaths(imageLayers),
	}}))
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return scanCatalogImage{path: path, tag: tag, diffIDs: diffIDs, files: files}
}

// normalizedName is the name a daemon's own `docker save` writes into the
// annotation containerd names an imported image by: the familiar one with the
// registry and the library namespace it implies spelled out.
func normalizedName(tag string) string {
	name, _, _ := strings.Cut(tag, ":")
	if !strings.Contains(name, "/") {
		name = "docker.io/library/" + name
	}
	return name + tag[strings.LastIndex(tag, ":"):]
}

// layerBlobPaths is the image's layers as an archive's own manifest lists
// them: one path per diff ID, in the image's order, so a layer two steps made
// appears once per step -- the loader holds that list's length against the
// config's, and a load that listed the shared layer once would be refused.
func layerBlobPaths(layers []ociDescriptor) []string {
	paths := make([]string, 0, len(layers))
	for _, layer := range layers {
		paths = append(paths, "blobs/sha256/"+strings.TrimPrefix(layer.Digest, "sha256:"))
	}
	return paths
}

// ociBlob is one blob of the fixture image: the bytes, and the digest the tar
// names them by.
type ociBlob struct {
	digest string
	body   []byte
}

type ociDescriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Platform    *ociPlatform      `json:"platform,omitempty"`
}

type ociPlatform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type ociIndex struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Manifests     []ociDescriptor `json:"manifests"`
}

type ociManifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Config        ociDescriptor   `json:"config"`
	Layers        []ociDescriptor `json:"layers"`
}

type ociConfig struct {
	Created      time.Time        `json:"created"`
	Architecture string           `json:"architecture"`
	OS           string           `json:"os"`
	Config       ociRuntimeConfig `json:"config"`
	RootFS       ociRootFS        `json:"rootfs"`
	History      []ociHistory     `json:"history"`
}

type ociRuntimeConfig struct {
	Cmd []string `json:"Cmd"`
	Env []string `json:"Env"`
}

type ociRootFS struct {
	Type    string   `json:"type"`
	DiffIDs []string `json:"diff_ids"`
}

type ociHistory struct {
	Created   time.Time `json:"created"`
	CreatedBy string    `json:"created_by"`
}

// dockerManifestItem is one entry of the manifest.json a save carries for the
// daemons that read it rather than the layout around it. The fields are the
// loader's own, and the list of layers is not optional: it is held against the
// config's diff IDs, one entry each.
type dockerManifestItem struct {
	Config   string   `json:"Config"`
	RepoTags []string `json:"RepoTags"`
	Layers   []string `json:"Layers"`
}

// ociJSON marshals one part of the layout, failing the test rather than
// writing a tar a daemon will refuse.
func ociJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// gzipOf is body as a docker save hands a layer over: the codec a daemon keeps
// a pushed layer in, and the one the scan reads only through a program.
func gzipOf(t *testing.T, body []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

// TestScanReadsTheCatalogFixture holds the fixture the docker-tagged test
// loads, against the scan it is loaded for, with no daemon in the way: the
// same tar read as the image's save is cleared, the layer its config lists
// twice costs the scan nothing, and a worker with no program for the codec the
// layers are in is refused rather than left clearing nothing.
//
// It is here because a daemon is the one thing a test cannot bring with it:
// without this, a fixture a load would shape differently -- a blob the fence
// walks past, a layer written twice, a diff ID the count does not add up to --
// would pass on a machine that never loads it and fail on the nightly job that
// does. The docker test still says what no fixture can: that a real daemon
// gives the image back this way, and that the pinned Trivy really flags the two
// placeholders the allowances clear.
func TestScanReadsTheCatalogFixture(t *testing.T) {
	files := scanFixtureFiles(t)
	image := writeScanCatalogImage(t, scanCatalogLayers(t, files), "fugaro-scan-catalog:test", "linux/amd64")
	fixture := scanFixture{env: scanCatalogEnv, files: image.files, saveTar: image.path, diffIDs: image.diffIDs}

	out, _, err := runScan(t, fixture, "0", "--secrets", image.tag)
	if err != nil {
		t.Fatalf("the scan refused the fixture image it is loaded for: %v\n%s", err, out)
	}

	// The same image for a worker whose tar cannot decompress it: every tool
	// the scan runs is there, gzip is not, and the layers really are gzip.
	t.Setenv("PATH", pathWithoutProgram(t, "gzip"))
	out, _, err = runScan(t, fixture, "0", "--secrets", image.tag)
	if err == nil {
		t.Fatalf("the scan cleared an image whose layers it read none of:\n%s", out)
	}
	if !strings.Contains(out, "holds a layer this cannot decode") {
		t.Fatalf("the scan refused the image over something other than a layer it could not read: %v\n%s", err, out)
	}
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

// envWithPath is this process's environment with PATH set to path, in place
// rather than beside the PATH it was given: a duplicate leaves which of the
// two a program reads to the C library, so a caller that hands the scan a PATH
// with a program missing would be handing it one that still has it.
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

// pathWithoutProgram is a PATH offering every program this process's own PATH
// offers except the one named: the worker whose tar cannot decompress a layer
// a daemon hands over in that codec, which is the pair the fence's undecodable
// branch exists for. The tools the scan itself runs are checked for, so a PATH
// this cannot build fails the test rather than showing up as a scan that
// refused for the wrong reason.
func pathWithoutProgram(t *testing.T, omitted string) string {
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
			if name == omitted || seen[name] || entry.IsDir() {
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
