//go:build docker

package testutil

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// RequireDocker skips the test when no Docker daemon answers.
func RequireDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		t.Skipf("docker is not available: %v", err)
	}
}

// DockerOutput runs docker and returns its stdout, failing the test on error.
func DockerOutput(t *testing.T, args ...string) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, Tail(stderr.String()))
	}
	return stdout.Bytes()
}

// Docker runs docker and returns its trimmed stdout, failing the test on error.
func Docker(t *testing.T, args ...string) string {
	t.Helper()
	return strings.TrimSpace(string(DockerOutput(t, args...)))
}

// DockerPlatform is the daemon's native platform, such as linux/arm64, so
// test images build without emulation.
func DockerPlatform(t *testing.T) string {
	t.Helper()
	return "linux/" + Docker(t, "version", "--format", "{{.Server.Arch}}")
}

var (
	baseOnce  sync.Once
	baseImage string
	baseErr   error
)

// BaseImage returns the web-node base image built from this checkout with
// images/build-base.sh, once per test binary, for the daemon's platform.
// FUGARO_TEST_BASE_IMAGE names a prebuilt image to use instead. Derived
// builds start FROM this local image, so the active docker builder must see
// local images, as Docker's default builder does.
func BaseImage(t *testing.T) string {
	t.Helper()
	RequireDocker(t)
	if img := os.Getenv("FUGARO_TEST_BASE_IMAGE"); img != "" {
		return img
	}
	platform := DockerPlatform(t)
	baseOnce.Do(func() {
		baseImage = "fugaro-web-node:test"
		cmd := exec.Command("sh", filepath.Join(ModuleRoot(), "images", "build-base.sh"), "web-node", baseImage)
		cmd.Env = append(os.Environ(), "PLATFORM="+platform)
		if out, err := cmd.CombinedOutput(); err != nil {
			baseErr = fmt.Errorf("building the base image: %v\n%s", err, Tail(string(out)))
		}
	})
	if baseErr != nil {
		t.Fatal(baseErr)
	}
	return baseImage
}

// LinuxBinary cross-compiles the Go package pkg for the daemon's platform
// into dir/name and returns its path.
func LinuxBinary(t *testing.T, pkg, dir, name string) string {
	t.Helper()
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+strings.TrimPrefix(DockerPlatform(t), "linux/"), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, b)
	}
	return out
}

// ShareWithContainer lets a container running as uid 1000 write under dir.
// That matters on Linux hosts, where the test runs as another uid. It makes
// everything under dir world-writable now (keeping execute bits), and
// registers a cleanup that does the same, as root in image, for whatever the
// container created, so t.TempDir can remove it. Call it once dir's contents
// are in place, and after the t.TempDir call that made dir, so this cleanup
// runs first.
func ShareWithContainer(t *testing.T, image, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm() | 0o666
		if d.IsDir() {
			mode = 0o777
		}
		return os.Chmod(p, mode)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "run", "--rm", "--user", "0", "--entrypoint", "chmod",
			"-v", dir+":"+dir, image, "-R", "a+rwX", dir).Run()
	})
}

// ImageContains reports whether needle appears in any file of the image's
// `docker save` export, looking inside gzip-compressed layers too, so it also
// finds data that a later layer deleted.
func ImageContains(t *testing.T, image, needle string) bool {
	t.Helper()
	cmd := exec.Command("docker", "save", image)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	found := false
	tr := tar.NewReader(stdout)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading docker save %s: %v", image, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		br := bufio.NewReaderSize(tr, 1<<16)
		var r io.Reader = br
		if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
			gz, err := gzip.NewReader(br)
			if err != nil {
				t.Fatalf("%s: %v", hdr.Name, err)
			}
			r = gz
		}
		ok, err := streamContains(r, []byte(needle))
		if err != nil {
			t.Fatalf("scanning %s: %v", hdr.Name, err)
		}
		found = found || ok
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("docker save %s: %v\n%s", image, err, stderr.String())
	}
	return found
}

// streamContains reports whether needle occurs in r, reading it in chunks.
func streamContains(r io.Reader, needle []byte) (bool, error) {
	buf := make([]byte, 1<<20)
	var tail []byte
	for {
		n, err := r.Read(buf)
		window := append(tail, buf[:n]...)
		if bytes.Contains(window, needle) {
			return true, nil
		}
		if keep := len(needle) - 1; len(window) > keep {
			tail = append([]byte(nil), window[len(window)-keep:]...)
		} else {
			tail = window
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
}

// NPMFixtureFiles are a package.json and package-lock.json with no
// dependencies, so `npm ci` succeeds offline and installs nothing.
func NPMFixtureFiles() map[string]string {
	return map[string]string{
		"package.json": `{ "name": "fugaro-fixture", "version": "0.0.0", "private": true }` + "\n",
		"package-lock.json": `{
  "name": "fugaro-fixture",
  "version": "0.0.0",
  "lockfileVersion": 3,
  "requires": true,
  "packages": {
    "": {
      "name": "fugaro-fixture",
      "version": "0.0.0"
    }
  }
}
`,
	}
}

// Tail returns the end of s, for failure messages that quote build logs.
func Tail(s string) string {
	const n = 6000
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
