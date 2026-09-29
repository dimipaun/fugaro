// Package imagecheck records what a workflow's derived image was built
// from, and decides whether it needs a rebuild (design §7.2).
//
// The build's record step writes a Record to the runs bucket, at
// RecordKey(slug, workflow). The daily check compares it with the base
// branch's head: the key files (KeyFiles) and the image config
// (ImageConfigHash) are computed the same way on both sides, from a Tree,
// which is a checkout directory in the build and a git tree in the check.
package imagecheck

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// RecordVersion is the Record schema version this package writes.
const RecordVersion = 1

// Record is what one derived image was built from, as the build's record
// step writes it to builds/<slug>/<workflow>/image.json.
type Record struct {
	Version  int       `json:"version"`
	Repo     string    `json:"repo"` // owner/name
	Workflow string    `json:"workflow"`
	BuiltAt  time.Time `json:"built_at"`
	BuildID  string    `json:"build_id"`

	SourceCommit string `json:"source_commit"`
	// SourceCommitTime is the source commit's committer time. The gate
	// compares these, since the build's shallow clone has no history.
	SourceCommitTime time.Time `json:"source_commit_time"`
	BaseBranch       string    `json:"base_branch"`

	KeyFiles        map[string]string `json:"key_files"` // path → git blob ID
	ImageConfigHash string            `json:"image_config_hash"`

	BaseRef     string `json:"base_ref"`     // the base image as the build was given it
	BaseDigest  string `json:"base_digest"`  // sha256:… of the base the build ran FROM
	Image       string `json:"image"`        // the image, without a tag
	ImageDigest string `json:"image_digest"` // sha256:… that latest was promoted to

	FugaroVersion string `json:"fugaro_version"`
	TemplateSalt  string `json:"template_salt"`
	// Adopted is reserved for a record written for an image the build
	// did not make; builds always write false.
	Adopted bool `json:"adopted"`
}

// RecordKey is the record's object in the runs bucket.
func RecordKey(slug, workflow string) string {
	return "builds/" + slug + "/" + workflow + "/image.json"
}

// NewerThan reports whether r describes a newer build than o: its source
// commit's committer time is later, or it is the same commit built later.
// Committer times need not follow history (clock skew, a rebase that
// keeps an old time); the check corrects a wrong answer, since a record
// whose commit isn't the branch head counts as changed.
func (r *Record) NewerThan(o *Record) bool {
	if r.SourceCommitTime.After(o.SourceCommitTime) {
		return true
	}
	return r.SourceCommit == o.SourceCommit && r.BuiltAt.After(o.BuiltAt)
}

// ParseRecord decodes a record. Unknown fields are ignored, so a record a
// newer fugaro wrote still reads.
func ParseRecord(data []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("the image record: %w", err)
	}
	if r.Version < 1 {
		return nil, errors.New("the image record has no version")
	}
	return &r, nil
}

// Tree is a repository tree at one commit. Production uses GitTree for
// both sides, the build's checkout (Open) and the check's clone (Clone),
// so both read git's own blob IDs. Paths are slash-separated and relative
// to the tree's root. A missing path is fs.ErrNotExist.
type Tree interface {
	// BlobID is git's object ID of the file at path.
	BlobID(path string) (string, error)
	// Glob returns the files matching a doublestar pattern, as cache key
	// globs match them.
	Glob(pattern string) ([]string, error)
	ReadFile(path string) ([]byte, error)
}

// KeyFiles are the workflow's cache key files in tree, with their blob
// IDs: every file its cache key globs match, or, when fugaro.yaml declares
// no cache, the key of the per-base default (config.DefaultCache).
func KeyFiles(cfg *config.Config, workflow string, tree Tree) (map[string]string, error) {
	w, ok := cfg.Workflows[workflow]
	if !ok {
		return nil, fmt.Errorf("fugaro.yaml has no workflow %q", workflow)
	}
	entries := w.Cache
	if len(entries) == 0 {
		var err error
		if entries, err = defaultCache(w.Base, tree); err != nil {
			return nil, fmt.Errorf("workflows.%s: %w", workflow, err)
		}
	}
	out := map[string]string{}
	for _, e := range entries {
		for _, pattern := range e.Key {
			matches, err := tree.Glob(pattern)
			if err != nil {
				return nil, fmt.Errorf("cache key %q: %w", pattern, err)
			}
			for _, m := range matches {
				id, err := tree.BlobID(m)
				switch {
				case errors.Is(err, fs.ErrNotExist):
					continue
				case err != nil:
					return nil, fmt.Errorf("cache key file %s: %w", m, err)
				}
				out[m] = id
			}
		}
	}
	return out, nil
}

// nodeDetectFiles are the files config.DetectNodePM reads (their content
// matters), and nodeLockfiles those it only looks for, in its order.
var (
	nodeDetectFiles = []string{"package.json", ".yarnrc.yml"}
	nodeLockfiles   = []string{"pnpm-lock.yaml", "yarn.lock", "package-lock.json", "npm-shrinkwrap.json"}
)

// defaultCache is config.DefaultCache for tree: the few files the package
// manager's detection reads are copied into a scratch directory (the
// lockfiles as empty files, since only their presence matters), so the
// check and the build detect exactly as the runner does.
func defaultCache(base string, tree Tree) ([]config.CacheEntry, error) {
	if base != "web-node" {
		return config.DefaultCache(base, "")
	}
	dir, err := os.MkdirTemp("", "fugaro-keyfiles-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	for _, name := range nodeDetectFiles {
		data, err := tree.ReadFile(name)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return nil, err
		}
	}
	for _, name := range nodeLockfiles {
		switch _, err := tree.BlobID(name); {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			return nil, err
		}
	}
	return config.DefaultCache(base, dir)
}

// imageConfig is the canonical form ImageConfigHash hashes: everything
// that makes the image wrong, rather than just stale, when it changes.
type imageConfig struct {
	Base           string        `json:"base"`
	Image          imageSettings `json:"image"`
	Dockerfile     string        `json:"dockerfile"`
	DockerfileBlob string        `json:"dockerfile_blob"`
}

type imageSettings struct {
	Node             string   `json:"node"`
	JDK              string   `json:"jdk"`
	Apt              []string `json:"apt"`
	Setup            []string `json:"setup"`
	SkipBuildScripts bool     `json:"skip_build_scripts"`
}

// ImageConfigHash is the hex SHA-256 of the canonical JSON of the
// workflow's base, image: settings, dockerfile: and, when that is set,
// the repository Dockerfile's blob ID in tree.
func ImageConfigHash(cfg *config.Config, workflow string, tree Tree) (string, error) {
	w, ok := cfg.Workflows[workflow]
	if !ok {
		return "", fmt.Errorf("fugaro.yaml has no workflow %q", workflow)
	}
	c := imageConfig{Base: w.Base, Dockerfile: w.Dockerfile, Image: imageSettings{
		Node: w.Image.Node, JDK: w.Image.JDK, SkipBuildScripts: w.Image.SkipBuildScripts,
		Apt: slices.Concat([]string{}, w.Image.Apt), Setup: slices.Concat([]string{}, w.Image.Setup),
	}}
	if w.Dockerfile != "" {
		id, err := tree.BlobID(w.Dockerfile)
		if err != nil {
			return "", fmt.Errorf("workflows.%s.dockerfile: %w", workflow, err)
		}
		c.DockerfileBlob = id
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return hashOf(data), nil
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
