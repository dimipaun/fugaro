package config

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

var (
	aptPackageRE = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+(=[A-Za-z0-9.+~:-]+)?$`)
	// miseToolRE allows a mise tool name such as node, python, npm:firebase-tools
	// or aqua:owner/repo. It also allows a key such as "a.b" or
	// "a/../../etc": a mise tool name may contain "." and "/" legitimately
	// (scoped npm packages, aqua/ubi owner/repo pairs), so this charset
	// cannot additionally rule out ".." path segments. Task 10's renderer,
	// which writes these keys into ~/.config/mise/config.toml, MUST quote
	// every TOML key it writes (a bare key is not safe here); that is
	// Task 10's test to add, not this one's.
	miseToolRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:@-]*$`)
	// miseVersionRE allows a mise tool version such as 24.19.0, 3.12 or
	// temurin-25. Like miseToolRE, this is a strict allowlist: every
	// character below is rejected by construction, not by a separate
	// denylist, which is what TestValidMiseTool pins against regression.
	miseVersionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~:-]*$`)
	// imageKeyFileRE is the shape an image.key_files entry must have. It
	// refuses by construction every character that would be read as
	// something other than itself further down: a glob character (the entry
	// becomes a doublestar pattern of the rebuild key), a backslash (git
	// reads it as an escape inside a pathspec, and Windows would read it as
	// a separator), and a control character (the entry is written into the
	// Dockerfile's checkout loop and into the refusal message). The rules it
	// cannot state — relative, cleaned, no "..", no ".git" — are checked one
	// at a time below, each with its own message.
	imageKeyFileRE = regexp.MustCompile(`^[^\x00-\x1f\x7f\\*?\[\]{}]+$`)
)

// maxMiseToolNameLen and maxMiseToolVersionLen bound an image.tools key and
// value: generous for any real mise tool or version, but small enough that
// neither the rendered Dockerfile nor the generated mise config can ever
// receive an oversized value.
const (
	maxMiseToolNameLen    = 128
	maxMiseToolVersionLen = 64
	// maxImageKeyFiles and maxImageKeyFilePath bound image.key_files: at
	// most 32 entries of at most 200 characters, so neither the Dockerfile's
	// checkout loop nor the rebuild key can be made large from a config.
	maxImageKeyFiles    = 32
	maxImageKeyFilePath = 200
)

// validMiseToolName and validMiseToolVersion are ValidMiseTool's two halves,
// kept separate so validateImage can report which one is wrong.
func validMiseToolName(name string) bool {
	return len(name) <= maxMiseToolNameLen && miseToolRE.MatchString(name)
}

func validMiseToolVersion(version string) bool {
	return len(version) <= maxMiseToolVersionLen && miseVersionRE.MatchString(version)
}

// ValidMiseTool reports whether name and version may be written into a mise
// config: nothing outside these characters, within these length caps, ever
// reaches a Dockerfile.
func ValidMiseTool(name, version string) bool {
	return validMiseToolName(name) && validMiseToolVersion(version)
}

// MiseTools is image.tools: a mise tool name to its version. Its
// UnmarshalYAML requires every version to be a string scalar, so a trap such
// as "python: 3.10" (an unquoted YAML float, which a human reads as the
// version "3.10" but YAML resolves as a number) is refused instead of
// silently stringified. schemas/fugaro.schema.json already requires a JSON
// string here; this keeps the Go parser at least as strict, not looser.
type MiseTools map[string]string

// UnmarshalYAML refuses a tools: entry whose value was not written as a
// string scalar (quoted, or a plain scalar YAML does not resolve to a
// number, bool or null, such as temurin-25). validateImage's regex and
// length checks run afterwards on the resulting strings; this only rejects
// the wrong YAML shape before that.
func (t *MiseTools) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: image.tools must be a mapping of tool name to version", value.Line)
	}
	m := make(MiseTools, len(value.Content)/2)
	for i := 0; i+1 < len(value.Content); i += 2 {
		k, v := value.Content[i], value.Content[i+1]
		if v.Tag != "!!str" {
			return fmt.Errorf("line %d: image.tools.%s must be a quoted string version, such as \"%s\" (quote it)", v.Line, k.Value, v.Value)
		}
		m[k.Value] = v.Value
	}
	*t = m
	return nil
}

// validateImage checks a workflow's image and dockerfile settings (design
// §5.1). p is the workflow's problem path, such as "workflows.web". Each
// setup step becomes one Dockerfile RUN line, so anything that would change
// the Dockerfile's structure (a newline, a heredoc, a trailing backslash) is
// refused rather than rendered.
func validateImage(p string, w Workflow) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	img := w.Image
	if !img.IsZero() && w.Dockerfile != "" {
		add(p+".dockerfile", "image and dockerfile are mutually exclusive; move the image settings into the Dockerfile")
	}
	if img.Node != "" {
		add(p+".image.node", "was removed in 0.7.0: declare node in mise.toml ([tools] node = %q) or in image.tools (%s)", pluginwire.Printable(img.Node), MigrationDoc)
	}
	if img.JDK != "" {
		add(p+".image.jdk", "was removed in 0.7.0: declare Java in mise.toml ([tools] java = %q) (%s)", "temurin-"+pluginwire.Printable(img.JDK), MigrationDoc)
	}
	for _, name := range sortedKeys(img.Tools) {
		tp := p + ".image.tools." + name
		switch v := img.Tools[name]; {
		case !validMiseToolName(name):
			add(tp, "must be a mise tool name such as node, python or npm:firebase-tools")
		case !validMiseToolVersion(v):
			add(tp, "must be a version such as 24.19.0, 3.12 or temurin-25")
		}
	}
	for i, pkg := range img.Apt {
		if !aptPackageRE.MatchString(pkg) {
			add(fmt.Sprintf("%s.image.apt[%d]", p, i), "must be a package name, optionally pinned as name=version")
		}
	}
	validateKeyFiles(p, img.KeyFiles, add)
	for i, step := range img.Setup {
		sp := fmt.Sprintf("%s.image.setup[%d]", p, i)
		trimmed := strings.TrimSpace(step)
		switch {
		case trimmed == "":
			add(sp, "must not be empty")
		case strings.ContainsAny(step, "\r\n"):
			add(sp, "must be a single line; chain commands with && or call a script in the repository")
		case strings.Contains(step, "<<"):
			add(sp, "must not contain << (a Dockerfile heredoc); call a script in the repository instead")
		case strings.HasSuffix(trimmed, `\`):
			add(sp, "must not end with a backslash")
		case strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "["):
			add(sp, "must not start with - or [ (it would be read as a RUN flag or exec form, not a shell command)")
		}
	}
	if d := w.Dockerfile; d != "" && (strings.HasPrefix(d, "/") || slices.Contains(strings.Split(d, "/"), "..")) {
		add(p+".dockerfile", "must be a relative path inside the repository, such as .fugaro/%s.Dockerfile", strings.TrimPrefix(p, "workflows."))
	}
	return ps
}

// keyFilePathRules are image.key_files' rules, in the order validateKeyFiles
// reports them, so the first failure is the one a person reads, each with the
// message that names it. ValidKeyFilePath asks the same list, so a path that
// comes from somewhere else — a patch pnpm's manifest names, which the build
// checks out and keys the image on exactly as a hand-written entry — is
// judged by one statement of the rules and not a second copy of them.
var keyFilePathRules = []struct {
	ok  func(f string) bool
	msg string
}{
	{func(f string) bool { return strings.TrimSpace(f) != "" }, "must not be empty"},
	{utf8.ValidString, "must be text"},
	{func(f string) bool { return utf8.RuneCountInString(f) <= maxImageKeyFilePath },
		fmt.Sprintf("must be at most %d characters", maxImageKeyFilePath)},
	{imageKeyFileRE.MatchString, "must be a repository path holding no glob (* ? [ ] { }), no backslash and no control character"},
	{func(f string) bool { return !strings.HasPrefix(f, "/") }, "must be a relative path inside the repository, not an absolute one"},
	{func(f string) bool { return !slices.Contains(strings.Split(f, "/"), "..") }, "must not hold a .. segment: the path is checked out inside the repository it belongs to"},
	{func(f string) bool { return path.Clean(f) == f }, "must be a clean path: no trailing slash, no // and no . segment"},
	{func(f string) bool { return f != "." }, "must name a file or a directory, not the repository root: the root would check the whole checkout out early"},
	{func(f string) bool { return f != ".git" && !strings.HasPrefix(f, ".git/") }, "must not name anything inside .git, which is not part of the checkout"},
}

// ValidKeyFilePath reports whether f is a path image.key_files accepts: one
// clean repository-relative path, inside the repository, as the path it is.
func ValidKeyFilePath(f string) bool {
	for _, r := range keyFilePathRules {
		if !r.ok(f) {
			return false
		}
	}
	return true
}

// validateKeyFiles checks image.key_files: each entry names one path inside
// the repository, as the path it is. Every message names the entry by its
// index and never echoes its value: a value may hold a terminal escape
// sequence, and a refusal that printed one would put it in a log.
func validateKeyFiles(p string, files []string, add func(path, format string, args ...any)) {
	if len(files) > maxImageKeyFiles {
		add(p+".image.key_files", "must have at most %d entries", maxImageKeyFiles)
	}
	seen := make(map[string]int, len(files))
	for i, f := range files {
		kp := fmt.Sprintf("%s.image.key_files[%d]", p, i)
		for _, r := range keyFilePathRules {
			if !r.ok(f) {
				add(kp, "%s", r.msg)
				break
			}
		}
		if j, dup := seen[f]; dup {
			add(kp, "duplicates image.key_files[%d]", j)
			continue
		}
		seen[f] = i
	}
}

// SharedCheckoutSince is the first fugaro release that builds and refreshes a
// shared dependency-layer image: the release validateCheckout refuses
// checkout: shared until. It is a different fact from internal/cli's
// sharedSince, which names the release whose *runner* clones for a shared job
// (0.14.0): the runner half landed first, and the image such a job runs does
// not exist before this release.
const SharedCheckoutSince = "0.15.0"

// sharedCheckoutShipped is false until the release that actually builds and
// refreshes a shared dependency-layer image is tagged: nothing before it
// does, so a repository that declares checkout: shared gets a job that
// cannot run. Everything else the kind needs is already here — the
// CheckoutShared value, its key rules in validateCheckout, the schema's enum
// value, PlanWorkflowJob's acceptance of FUGARO_CHECKOUT=shared (which reads
// this gate through SharedCheckoutShipped) and the sharedSince launch gate —
// so the release that ships shared images flips this one value and nothing
// else, the way cloneShipped and catalogShipped (internal/cli/recipes_skew.go)
// were flipped.
var sharedCheckoutShipped = false

// SharedCheckoutShipped reports whether this build ships shared images.
// internal/infra reads it: validateCheckout refuses a config that declares
// checkout: shared while the gate is off, but a job's own environment is not a
// config, and a mark a person wrote into a Cloud Run job by hand would
// otherwise move that job's image to a shared one no build produces.
func SharedCheckoutShipped() bool { return sharedCheckoutShipped }

// SharedCheckoutRefusal is the problem validateCheckout reports for
// checkout: shared while sharedCheckoutShipped is false. It is exported so a
// launch, which reads the checkout's fugaro.yaml leniently, can find it among
// that file's problems and refuse with this sentence rather than with
// whatever later step trips over the config it could not use.
const SharedCheckoutRefusal = "shared images arrive in " + SharedCheckoutSince + ": no release builds or refreshes a shared dependency-layer image yet, so a shared workflow's job could not run; use checkout: baked to build a per-repository image, or checkout: clone to run the base image and clone the source at launch"

// validateCheckout checks a workflow's checkout: (design generic-tool.md §2,
// G3). clone runs the installation's base image directly, with no build, so
// a key that only a build can honor is refused, naming checkout: baked.
// shared runs a shared dependency-layer image and clones the repository's
// own source at launch (design forge-authorship-images/shared-images.md §6,
// route A), so it accepts exactly the keys that describe that layer — and
// refuses the ones that would put this repository's own content into an
// image every repository on the same dependency set runs (§1, §12).
//
// shared is refused outright while sharedCheckoutShipped is false: naming
// the kinds that work today is more use to a repository than a job that
// would fail at launch for want of an image nobody builds yet.
//
// image.setup is the design's own open question and this is where it is
// taken: §2.2 and §4 have the shared layer installed by whichever
// repository's image.setup first built the key, and accept that the others
// inherit that step, while §12 has the image hold the dependency layer "and
// only it". The stricter reading wins, because a setup command runs in the
// repository's checkout and its output is therefore that repository's
// content — the alternative lets a workflow declare shared and still bake
// its own work into a shared image. The cost is named where it is paid: a
// workflow moving to shared (§10) loses the step.
func validateCheckout(p string, w Workflow) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	switch w.Checkout {
	case CheckoutBaked, CheckoutClone, CheckoutShared:
	default:
		// The doubled "or" is deliberate: this enum names all three values it
		// accepts, and internal/config/layer_test.go pins the "must be baked
		// or clone" prefix of the same message for a profile's checkout:.
		add(p+".checkout", "must be baked or clone or shared; shared runs a shared dependency-layer image and clones the source at launch")
		return ps
	}
	if w.Checkout == CheckoutShared && !sharedCheckoutShipped {
		add(p+".checkout", SharedCheckoutRefusal)
		return ps
	}
	// A base: alongside clone or shared needs no refusal of its own here:
	// since the 0.7.0 cut every non-empty base: (legacy or not) is refused
	// by the caller with legacyBaseMessage, so neither kind can silently
	// ignore one.
	if w.Checkout == CheckoutShared {
		const msg = "checkout: shared runs an image shared by every repository on the same dependency set, which holds no repository's own content; use checkout: baked to build a per-repository image"
		if len(w.Image.Setup) > 0 {
			add(p+".image.setup", "%s", msg)
		}
		if w.Dockerfile != "" {
			add(p+".dockerfile", "%s", msg)
		}
		return ps
	}
	if w.Checkout != CheckoutClone {
		return ps
	}
	const msg = "checkout: clone runs the base image with no build; use checkout: baked to build an image"
	if len(w.Image.Apt) > 0 {
		add(p+".image.apt", "%s", msg)
	}
	if len(w.Image.Setup) > 0 {
		add(p+".image.setup", "%s", msg)
	}
	if len(w.Image.Tools) > 0 {
		add(p+".image.tools", "%s", msg)
	}
	if w.Image.SkipBuildScripts {
		add(p+".image.skip_build_scripts", "%s", msg)
	}
	if len(w.Image.KeyFiles) > 0 {
		add(p+".image.key_files", "%s", msg)
	}
	if w.Dockerfile != "" {
		add(p+".dockerfile", "%s", msg)
	}
	return ps
}
