package selfupgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// The failures a caller ever sees, one line each: an upgrade that cannot be
// fetched, verified or read ends the run naming the version (D3443), so
// what each of these says stays this short.
var (
	errDownload = errors.New("the download failed")
	errTooBig   = errors.New("the download is too large")
	errNoSum    = errors.New("the checksums do not name the archive")
	errMismatch = errors.New("the checksum does not match")
	errArchive  = errors.New("the archive is not a fugaro binary")
)

// upgrade fetches release want and installs its binary into a fresh
// read-only directory, returning the binary's path. It does nothing — no
// download, no error — for a development build on either side, or for a
// release that is not above the running one (D3449): it never downgrades,
// and it never falls back to a copy it could not verify.
func upgrade(ctx context.Context, want, have string) (string, error) {
	if !updatecheck.Newer(want, have) {
		return "", nil
	}
	version, ok := updatecheck.CanonicalRelease(want)
	if !ok {
		return "", nil
	}
	name := archiveName(version)
	checksums, err := fetchBytes(ctx, assetURL(version, "checksums.txt"), maxChecksumsBytes)
	if err != nil {
		return "", failed(version, err)
	}
	sum, ok := checksumFor(checksums, name)
	if !ok {
		return "", failed(version, errNoSum)
	}
	archive, err := fetchBytes(ctx, assetURL(version, name), maxArchiveBytes)
	if err != nil {
		return "", failed(version, err)
	}
	if err := verifyChecksum(archive, sum); err != nil {
		return "", failed(version, err)
	}
	exe, err := install(archive, version)
	if err != nil {
		return "", failed(version, err)
	}
	return exe, nil
}

// failed is the one line a run ends with: what could not happen, and the
// version it was for (D3443). The detail stays in err.
func failed(version string, err error) error {
	return fmt.Errorf("fugaro: could not upgrade to %s: %w", version, err)
}

// install unpacks archive into a fresh directory and makes both the
// directory and the binary read-only (F33). The directory is a temp one
// nothing else names: the job runs as one non-root user, so the binary
// lands where that user can write — /usr/local/bin, where the image's own
// copy sits, is not writable — and the mode is what keeps the user's own
// tools from being planted beside it.
func install(archive []byte, version string) (string, error) {
	dir, err := os.MkdirTemp("", "fugaro-"+version+"-")
	if err != nil {
		return "", errArchive
	}
	exe, err := extractFugaro(bytes.NewReader(archive), dir)
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if err := os.Chmod(exe, 0o555); err != nil {
		os.RemoveAll(dir)
		return "", errArchive
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		os.RemoveAll(dir)
		return "", errArchive
	}
	return exe, nil
}

// extractFugaro writes the archive's one regular `fugaro` file into dir and
// returns its path. Everything else is refused: a link or a device node,
// and any name that leaves dir. Only one file of a fetched archive is ever
// wanted, so the extractor never writes where a name says.
func extractFugaro(r io.Reader, dir string) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", errArchive
	}
	defer gz.Close()
	exe := ""
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", errArchive
		}
		if !archivePathOK(h.Name) {
			return "", errArchive
		}
		switch h.Typeflag {
		case tar.TypeReg:
			if path.Clean(h.Name) != binaryName {
				continue
			}
			if exe != "" {
				return "", errArchive
			}
			if exe, err = writeBinary(tr, filepath.Join(dir, binaryName)); err != nil {
				return "", errArchive
			}
		case tar.TypeDir:
			continue
		default:
			return "", errArchive
		}
	}
	if exe == "" {
		return "", errArchive
	}
	return exe, nil
}

// writeBinary writes r to dst, which nothing may already hold.
func writeBinary(r io.Reader, dst string) (string, error) {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return dst, nil
}

// archivePathOK reports whether a tar name is a relative path that stays
// inside the extraction directory. Names are slash-separated, so path
// rather than filepath is the cleaning that applies.
func archivePathOK(name string) bool {
	if name == "" || path.IsAbs(name) {
		return false
	}
	clean := path.Clean(name)
	return clean != ".." && !strings.HasPrefix(clean, "../")
}
