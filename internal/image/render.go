// Package image renders, builds and smoke-tests a workflow's derived image
// (design §7.2).
package image

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// RenderInput is what the derived-image template is rendered from.
type RenderInput struct {
	Workflow string         // workflow name, for the header
	Base     string         // the workflow's base
	Image    config.Image   // the workflow's image: settings
	PM       *config.NodePM // the checkout's Node package manager; nil skips the warm-up
	Mise     bool           // install the checkout's mise config and image.tools
	Secrets  []string       // workflow secret variables, mounted into the warm-up and setup steps
	Version  string         // fugaro version, for the header
	// KeyFiles are the repository paths the dependency install reads — the
	// root mise config, its lockfile, the workflow's key files
	// (imagecheck.KeyFiles) and the manifest the warm-up was chosen from —
	// which the key-file layer checks out at FUGARO_COMMIT, before the source
	// layer. Dockerfile fills them in from the checkout; an empty list is a
	// checkout with nothing to check out early.
	KeyFiles []string
}

var derived = template.Must(template.New("Dockerfile").
	Funcs(template.FuncMap{"join": strings.Join}).
	Parse(images.DerivedTemplate))

// Render renders images/derived/Dockerfile.tmpl for one workflow.
func Render(in RenderInput) ([]byte, error) {
	if in.Image.Node != "" {
		return nil, fmt.Errorf("image.node was removed in 0.7.0 (%s)", config.MigrationDoc)
	}
	for i, step := range in.Image.Setup {
		if t := strings.TrimSpace(step); strings.HasPrefix(t, "-") || strings.HasPrefix(t, "[") {
			return nil, fmt.Errorf("image.setup[%d] must not start with - or [ (it would be read as a RUN flag or exec form, not a shell command): %q", i, step)
		}
	}
	data := struct {
		RenderInput
		WarmUp, WarmUpFor, SecretMounts, SecretEnv string
		ToolLines                                  []string
		ShellKeyFiles                              string
	}{RenderInput: in}
	data.ShellKeyFiles = shellWords(in.KeyFiles)
	if in.PM != nil {
		data.WarmUp, data.WarmUpFor = in.PM.WarmUp(in.Image.SkipBuildScripts), in.PM.Lockfile
	}
	for _, name := range slices.Sorted(maps.Keys(in.Image.Tools)) {
		v := in.Image.Tools[name]
		if !config.ValidMiseTool(name, v) {
			return nil, fmt.Errorf("image.tools.%s: %q is not a mise tool and version this template can write", name, v)
		}
		data.ToolLines = append(data.ToolLines, fmt.Sprintf("%q = %q", name, v))
	}
	data.SecretMounts, data.SecretEnv = secretMounts(in.Secrets)
	var buf bytes.Buffer
	if err := derived.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("rendering the derived-image template: %w", err)
	}
	return buf.Bytes(), nil
}

// secretMounts returns the RUN flags that mount each workflow secret and the
// shell prefix that exports it, for the warm-up and setup steps.
//
// Each secret is mounted as a file, /run/secrets/<VAR>, readable only by
// fugaro (uid 1000, which those steps run as), and the prefix reads it into
// <VAR> inside the RUN's own shell, so the value is on no command line and
// in no layer, ENV, build argument or log. A secret the build lacks is not
// mounted (required=false) and its variable stays unset; one that is
// mounted but unreadable fails the step. Like any $(…), the read drops the
// value's trailing newlines. BuildKit's env=
// secret mounts would be simpler, but Cloud Build's docker daemon refuses
// them ("requested experimental feature exec.secretenv is not supported").
// vars are environment variable names (config validates them), so they
// need no quoting.
func secretMounts(vars []string) (mounts, prefix string) {
	for _, v := range vars {
		mounts += fmt.Sprintf(" --mount=type=secret,id=%s,uid=1000,mode=0400,required=false", v)
		prefix += fmt.Sprintf(`if test -e /run/secrets/%[1]s; then %[1]s="$(cat /run/secrets/%[1]s)" || exit 1; export %[1]s; fi; `, v)
	}
	return mounts, prefix
}

// Dockerfile returns the Dockerfile that builds workflow name's derived image
// from the checkout at root: the repository's own when the workflow sets
// dockerfile:, whose path is then returned as repoFile, or else the rendered
// template, with repoFile empty.
func Dockerfile(root string, cfg *config.Config, name, version string) (data []byte, repoFile string, err error) {
	w, ok := cfg.Workflows[name]
	if !ok {
		return nil, "", fmt.Errorf("fugaro.yaml has no workflow %q", name)
	}
	if w.Dockerfile != "" {
		data, err := config.ReadRegular(filepath.Join(root, w.Dockerfile), config.MaxCheckoutFile)
		if err != nil {
			return nil, "", fmt.Errorf("workflows.%s.dockerfile: %w", name, err)
		}
		return data, w.Dockerfile, nil
	}
	in := RenderInput{Workflow: name, Base: w.BaseKind(), Image: w.Image, Version: version}
	for _, s := range w.Secrets {
		in.Secrets = append(in.Secrets, s.Env)
	}
	if in.PM, err = config.DetectNodePM(root); err != nil {
		return nil, "", fmt.Errorf("workflows.%s: %w", name, err)
	}
	files, err := config.MiseConfigFiles(root)
	if err != nil {
		return nil, "", fmt.Errorf("workflows.%s: reading the mise config: %w", name, err)
	}
	in.Mise = len(files) > 0 || len(w.Image.Tools) > 0
	in.KeyFiles = files
	// mise reads its lockfile when the checkout has one, and it pins what
	// mise installs: MiseConfigFiles leaves it out (it declares no tool, and
	// the image config hash covers it separately), so it is added here.
	if fi, err := os.Lstat(filepath.Join(root, config.MiseLockfile)); err == nil && fi.Mode().IsRegular() {
		in.KeyFiles = append(in.KeyFiles, config.MiseLockfile)
	}
	// package.json is what the warm-up was chosen from and what every Node
	// install reads: npm ci refuses a checkout whose lock file has no
	// package.json beside it ("lock file's app@1.0.0 does not satisfy
	// app@"), and pnpm and yarn read the dependency specs and the workspace
	// list from it. The key files alone do not carry it (a Node workflow with
	// no cache: is keyed by its lockfile only), so the install that reads it
	// above the source layer would otherwise have nothing to read.
	if in.PM != nil {
		in.KeyFiles = append(in.KeyFiles, "package.json")
	}
	if in.PM != nil && in.PM.Name == "yarn" && in.PM.Berry {
		// Yarn Berry reads .yarnrc.yml, and it decided the cache mode above.
		in.KeyFiles = append(in.KeyFiles, ".yarnrc.yml")
	}
	keys, err := imagecheck.KeyFiles(cfg, name, checkoutTree{root})
	if err != nil {
		return nil, "", fmt.Errorf("workflows.%s: %w", name, err)
	}
	in.KeyFiles = append(in.KeyFiles, slices.Sorted(maps.Keys(keys))...)
	slices.Sort(in.KeyFiles)
	in.KeyFiles = slices.Compact(in.KeyFiles)
	data, err = Render(in)
	return data, "", err
}

// checkoutTree is the imagecheck.Tree of a plain checkout directory, as
// KeyFiles is computed over for a rebuild. The build's own checkout is read
// as a git tree (imagecheck.Open), but the render runs where it stands, and
// its blob IDs are unused: only the paths are.
type checkoutTree struct{ root string }

func (c checkoutTree) file(path string) (string, error) {
	if !fs.ValidPath(path) {
		return "", fmt.Errorf("%s: %w", path, fs.ErrNotExist)
	}
	full := filepath.Join(c.root, filepath.FromSlash(path))
	// A symlink is not a key file, as the cache's own key leaves it out.
	fi, err := os.Lstat(full)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file: %w", path, fs.ErrNotExist)
	}
	return full, nil
}

func (c checkoutTree) BlobID(path string) (string, error) {
	data, err := c.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Glob matches files only, and doesn't follow symlinks.
func (c checkoutTree) Glob(pattern string) ([]string, error) {
	return doublestar.Glob(os.DirFS(c.root), pattern, doublestar.WithFilesOnly(), doublestar.WithNoFollow())
}

func (c checkoutTree) ReadFile(path string) ([]byte, error) {
	full, err := c.file(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}

// shellWords renders each word as one single-quoted sh word, so a path with
// a space or a quote reaches git as itself.
func shellWords(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(out, " ")
}
