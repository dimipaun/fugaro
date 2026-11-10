package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// DefaultImageInfoPath is where a derived image records its build time and
// the commit it baked in.
const DefaultImageInfoPath = "/etc/fugaro/image.json"

// maxImageInfoBytes bounds the file read: it is a few dozen bytes.
const maxImageInfoBytes = 64 << 10

// imageFile is /etc/fugaro/image.json, as the image's last build step
// writes it.
type imageFile struct {
	BuiltAt string `json:"built_at"`
	Commit  string `json:"commit"`
}

// imageInfo reads what the image was built from: the baked checkout's HEAD
// as it is now, before the sync changes it, and the build time from the
// image's own record. It is never fatal: a problem is logged, and what
// could not be read is left out. It returns nil when the commit is unknown.
//
// The commit it reads is the one the checkout at the work directory is at when
// it is called, and bootstrap reads that a second time — bakedHead, once the
// run's own checkout is adopted — because the commit the record has to name is
// the adopted one's (bootstrap's comment there).
func (r *run) imageInfo(ctx context.Context) *runstore.ImageInfo {
	var info runstore.ImageInfo
	path := r.d.ImageInfoPath
	if path == "" {
		path = DefaultImageInfoPath
	}
	var file imageFile
	switch data, err := readCapped(path, maxImageInfoBytes); {
	case errors.Is(err, os.ErrNotExist):
		// An image built before this record existed, or a local run.
	case err != nil:
		r.d.Log.Warn("reading the image's build record failed", "path", path, "err", r.redact(err.Error()))
	default:
		if err := json.Unmarshal(data, &file); err != nil {
			r.d.Log.Warn("the image's build record is not valid", "path", path, "err", r.redact(err.Error()))
		} else if file.BuiltAt != "" {
			if t, err := time.Parse(time.RFC3339, file.BuiltAt); err != nil {
				r.d.Log.Warn("the image's build time is not valid", "path", path, "err", r.redact(err.Error()))
			} else {
				t = t.UTC()
				info.BuiltAt = &t
			}
		}
	}
	if sha := r.bakedHead(ctx); sha != "" {
		info.BakedCommit = sha
	}
	if info.BakedCommit == "" {
		info.BakedCommit = file.Commit
	}
	if info.BakedCommit == "" {
		return nil
	}
	return &info
}

// bakedHead is the HEAD of the checkout at the run's work directory, or "" when
// there is none to read: only an existing checkout says anything, and a run
// that must clone has no baked commit.
//
// Which checkout that is changes while bootstrap runs: the work directory holds
// the image's own checkout, or nothing at all, until openBakedCheckout adopts
// the per-slug one this run's repository was baked at (D1900), and only then is
// the checkout the run works on known.
func (r *run) bakedHead(ctx context.Context) string {
	repo, err := gitops.Open(r.d.WorkDir, gitops.IdentityEnv())
	if err != nil {
		return ""
	}
	sha, err := strip(repo).HeadSHA(ctx)
	if err != nil {
		r.d.Log.Warn("reading the baked checkout's HEAD failed", "err", r.redact(err.Error()))
		return ""
	}
	return sha
}

// readCapped reads the file at path, failing if it is larger than max.
func readCapped(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, max)
	}
	return data, nil
}
