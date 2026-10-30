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
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

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
	// Key is the repository's normalised dependency-set key (Key), computed
	// from those same key files. It is empty for a repository whose key files
	// do not reduce: such a repository is unkeyed (design §2.5), keeps its
	// own per-(repository, workflow) image and is on no shared one.
	Key string `json:"key,omitempty"`

	BaseRef     string `json:"base_ref"`     // the base image as the build was given it
	BaseDigest  string `json:"base_digest"`  // sha256:… of the base the build ran FROM
	Image       string `json:"image"`        // the image, without a tag
	ImageDigest string `json:"image_digest"` // sha256:… that latest was promoted to

	FugaroVersion string `json:"fugaro_version"`
	// Edition is the edition of the binary that rendered this record — "core" or
	// "ee", as internal/cli/edition.go reports it. It is empty only in a record
	// written before this field existed: a binary that ran the render step
	// always knows its own.
	Edition string `json:"edition,omitempty"`
	// SealedBudgetToken is true when the fugaro that rendered this record
	// reads the sealed budget token (docs/design/budget-token-channel.md
	// §7.1). That fugaro is the BASE image's own binary, which runs the image
	// build's render step (images/derived/cloudbuild.yaml): the field says
	// only that the base image's own build step recorded it. It does not say
	// which fugaro the launched image runs: a dockerfile: workflow's image is
	// the repository's to build, its fugaro included. A launch that compares
	// ImageDigest with the image's latest tag reports drift between the two,
	// never what binary the image carries, and base_ref's release gate judges
	// the base image, not this one. The launcher's seal gate trusts the field
	// as far as that goes; a record without it says nothing either way.
	SealedBudgetToken bool   `json:"sealed_budget_token,omitempty"`
	TemplateSalt      string `json:"template_salt"`
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

// DigestMismatch reports whether live, the digest the workflow's latest tag
// resolves to now, is not the digest ImageDigest records the build
// promoted: the drift the daily check rebuilds for (ReasonLatestDrift). A
// launch warns about it and goes on, since Cloud Run cannot vary the image
// per execution and nothing pins the digest a build recorded (D619). A
// record that names no digest, and an unknown live digest, are nothing to
// compare rather than a mismatch: neither then says anything about the
// image a job runs.
func (r *Record) DigestMismatch(live string) bool {
	if r == nil || r.ImageDigest == "" || live == "" {
		return false
	}
	return live != r.ImageDigest
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

// NodeInstallPaths are the repository paths beyond the lockfiles that a
// Node dependency install reads, and so the paths a change to must rebuild
// the image exactly as a lockfile change does:
//
//   - .yarnrc and .pnpmfile.cjs, the Yarn 1 and pnpm configuration files
//     their installs read;
//   - .yarn/releases, .yarn/plugins and .yarn/patches, which a Yarn Berry
//     repository commits: the Yarn build .yarnrc.yml's yarnPath names, the
//     plugins it loads, and the patches a patch: resolution applies.
//
// The last three are directories: installPatterns matches each path both as
// itself and as its subtree, so a repository that keeps any of these
// contributes every file under it.
var NodeInstallPaths = []string{".yarnrc", ".pnpmfile.cjs", ".yarn/releases", ".yarn/plugins", ".yarn/patches"}

// installPatterns are the tree globs covering each path: the path itself,
// and everything under it, since a path may name a directory. A path that
// names a file matches the first glob only.
func installPatterns(paths []string) []string {
	out := make([]string, 0, 2*len(paths))
	for _, p := range paths {
		out = append(out, p, p+"/**")
	}
	return out
}

// PnpmPatches are the patch files the repository's pnpm manifests name, as
// repository-relative paths: pnpm reads patchedDependencies from
// pnpm-workspace.yaml or from package.json's pnpm field, and applies each
// patch at install time, so a change to one changes what the install
// produces.
func PnpmPatches(tree Tree) ([]string, error) {
	var out []string
	switch data, err := tree.ReadFile("pnpm-workspace.yaml"); {
	case err == nil:
		var doc struct {
			Patched map[string]string `yaml:"patchedDependencies"`
		}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("pnpm-workspace.yaml: %w", err)
		}
		out = append(out, slices.Sorted(maps.Values(doc.Patched))...)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("pnpm-workspace.yaml: %w", err)
	}
	switch data, err := tree.ReadFile("package.json"); {
	case err == nil:
		var pkg struct {
			Pnpm struct {
				Patched map[string]string `json:"patchedDependencies"`
			} `json:"pnpm"`
		}
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil, fmt.Errorf("package.json: %w", err)
		}
		out = append(out, slices.Sorted(maps.Values(pkg.Pnpm.Patched))...)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("package.json: %w", err)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// KeyFiles are the workflow's cache key files in tree, with their blob
// IDs: every file its cache key globs match, or, when fugaro.yaml declares
// no cache, the key of the per-base default (config.DefaultCache). The
// dependency install's own inputs join them — the install files of
// NodeInstallPaths, the patch files PnpmPatches names, and the paths
// image.key_files asks for — whether or not the workflow declares a cache:,
// since a change to one of them changes the image just as a lockfile change
// does.
func KeyFiles(cfg *config.Config, workflow string, tree Tree) (map[string]string, error) {
	w, ok := cfg.Workflows[workflow]
	if !ok {
		return nil, fmt.Errorf("fugaro.yaml has no workflow %q", workflow)
	}
	entries := w.Cache
	if len(entries) == 0 {
		var err error
		if entries, err = defaultCache(tree); err != nil {
			return nil, fmt.Errorf("workflows.%s: %w", workflow, err)
		}
	}
	patches, err := PnpmPatches(tree)
	if err != nil {
		return nil, fmt.Errorf("workflows.%s: %w", workflow, err)
	}
	install := append(slices.Clone(NodeInstallPaths), w.Image.KeyFiles...)
	install = append(install, patches...)
	out := map[string]string{}
	for _, e := range entries {
		if err := addKeyFiles(out, tree, "cache key", e.Key); err != nil {
			return nil, err
		}
	}
	if err := addKeyFiles(out, tree, "install", installPatterns(install)); err != nil {
		return nil, err
	}
	return out, nil
}

// addKeyFiles records every file the patterns match in tree, with its blob
// ID. what names the patterns in an error.
func addKeyFiles(out map[string]string, tree Tree, what string, patterns []string) error {
	for _, pattern := range patterns {
		matches, err := tree.Glob(pattern)
		if err != nil {
			return fmt.Errorf("%s %q: %w", what, pattern, err)
		}
		for _, m := range matches {
			id, err := tree.BlobID(m)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				continue
			case err != nil:
				return fmt.Errorf("%s file %s: %w", what, m, err)
			}
			out[m] = id
		}
	}
	return nil
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
func defaultCache(tree Tree) ([]config.CacheEntry, error) {
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
	return config.DefaultCache(dir)
}

// imageConfig is the canonical form ImageConfigHash hashes: everything
// that makes the image wrong, rather than just stale, when it changes.
type imageConfig struct {
	Base           string            `json:"base"`
	Image          imageSettings     `json:"image"`
	Dockerfile     string            `json:"dockerfile"`
	DockerfileBlob string            `json:"dockerfile_blob"`
	Tools          map[string]string `json:"tools,omitempty"`
	MiseFiles      map[string]string `json:"mise_files,omitempty"`
}

type imageSettings struct {
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
	c := imageConfig{Base: w.BaseKind(), Dockerfile: w.Dockerfile, Image: imageSettings{
		SkipBuildScripts: w.Image.SkipBuildScripts,
		Apt:              slices.Concat([]string{}, w.Image.Apt), Setup: slices.Concat([]string{}, w.Image.Setup),
	}, Tools: w.Image.Tools}
	if w.Dockerfile != "" {
		id, err := tree.BlobID(w.Dockerfile)
		if err != nil {
			return "", fmt.Errorf("workflows.%s.dockerfile: %w", workflow, err)
		}
		c.DockerfileBlob = id
	}
	files, err := miseFiles(tree)
	if err != nil {
		return "", fmt.Errorf("workflows.%s: %w", workflow, err)
	}
	if len(files) > 0 {
		c.MiseFiles = files
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return hashOf(data), nil
}

// miseFiles are the root mise config files and lockfile in tree, with their
// blob IDs: what the derived image's mise install read. A nil tree (doctor's
// local-only call, which has no git tree to read: doctor_layer.go's own
// comment on its Dockerfile guard says why) reports no mise files rather
// than panicking on a nil Tree's method set.
func miseFiles(tree Tree) (map[string]string, error) {
	if tree == nil {
		return nil, nil
	}
	out := map[string]string{}
	add := func(p string) error {
		id, err := tree.BlobID(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return fmt.Errorf("%s: %w", p, err)
		}
		out[p] = id
		return nil
	}
	for _, p := range append(slices.Clone(config.MiseConfigPaths), config.MiseLockfile) {
		if err := add(p); err != nil {
			return nil, err
		}
	}
	for _, d := range config.MiseConfigDirs {
		matches, err := tree.Glob(d + "/*.toml")
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if strings.HasPrefix(path.Base(m), ".") {
				continue
			}
			if err := add(m); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
