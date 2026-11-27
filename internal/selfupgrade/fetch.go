// Package selfupgrade replaces the running fugaro with the release a task
// was stamped with (spec §6, D3442). The runner in a job image is the
// image's own binary, and a launcher newer than the image stamps a version
// the image does not carry; upgrading in place is what closes that gap
// without waiting for a rebuild (F29, F31).
//
// The archive comes from this repository's GitHub release over TLS and is
// installed only when its sha256 matches the digest the release's own
// checksums.txt states (D3442) — no signature reaches a job image (F32), so
// that check and the fixed repository are the whole of the trust in the
// bytes. The installed directory and the binary are made read-only (F33),
// and a development build never upgrades in either direction (D3449).
package selfupgrade

import (
	"context"
	"io"
	"net/http"
	"runtime"
	"time"

	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// releaseBaseURL is where a release's assets live: the repository
// updatecheck already names, so the host and path cannot drift from the one
// the update check trusts. Nothing a task carries reaches it.
const releaseBaseURL = "https://github.com/" + updatecheck.Repo + "/releases/download/"

// releaseBase is releaseBaseURL, and a variable only so a test can point it
// at an httptest server.
var releaseBase = releaseBaseURL

// binaryName is the one file an archive is read for. The archive is the
// mixed-licence `fugaro_` build and never the pure-MIT `fugaro-core_` one:
// the runner carries the ee code a job needs, and goreleaser names both
// archives after the binary inside them (`.goreleaser.yaml`).
const binaryName = "fugaro"

// maxArchiveBytes caps a release archive: a fugaro tarball is tens of MB,
// and a larger body is a broken or hostile response rather than something
// to read into memory.
const maxArchiveBytes = 256 << 20

// maxChecksumsBytes caps checksums.txt: it lists a dozen archives, so it is
// a few KB.
const maxChecksumsBytes = 64 << 10

// fetchAttempts and retryDelay are the whole of "retries briefly" (D3443):
// a job whose network is not up yet is the case they cover, and nothing
// waits more than a second for a release.
const fetchAttempts = 3

const retryDelay = 200 * time.Millisecond

// client bounds one response rather than the whole upgrade, which the
// caller's context bounds: an archive is tens of MB, so its timeout is
// minutes rather than updatecheck's one second.
var client = &http.Client{Timeout: 5 * time.Minute}

// archiveName is the archive goreleaser writes for a job image: the
// mixed-licence build, for linux and this machine's architecture. version
// is the canonical X.Y.Z, which is what goreleaser's `{{ .Version }}`
// holds, while the tag it hangs under keeps its "v".
func archiveName(version string) string {
	return "fugaro_" + version + "_linux_" + runtime.GOARCH + ".tar.gz"
}

// assetURL is one release asset's URL. version came out of updatecheck's
// parser, so nothing but a plain X.Y.Z can reach the path.
func assetURL(version, name string) string {
	return releaseBase + "v" + version + "/" + name
}

// fetchBytes downloads one release asset, bounded by max, retrying briefly.
func fetchBytes(ctx context.Context, url string, max int64) ([]byte, error) {
	var err error
	for attempt := 0; attempt < fetchAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, errDownload
			case <-time.After(retryDelay):
			}
		}
		var data []byte
		if data, err = download(ctx, url, max); err == nil {
			return data, nil
		}
	}
	return nil, err
}

// download reads one response whole, refusing a body over max rather than
// buffering it: the LimitReader stops at max+1 bytes, so a response of any
// size costs the cap and one byte.
func download(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errDownload
	}
	req.Header.Set("User-Agent", "fugaro-selfupgrade")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errDownload
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errDownload
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, errDownload
	}
	if int64(len(data)) > max {
		return nil, errTooBig
	}
	return data, nil
}
