package config

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

var (
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
	// imageKeyFileRE is the shape a checkout path must have. It refuses by
	// construction every character that would be read as something other
	// than itself further down: a glob character (a path becomes a doublestar
	// pattern where the checkout keys on it), a backslash (git reads it as an
	// escape inside a pathspec, and Windows would read it as a separator), and
	// a control character (a path is written into the image's checkout and
	// into a refusal message). The rules it cannot state — relative, cleaned,
	// no "..", no ".git" — are checked one at a time below, each with its own
	// message.
	imageKeyFileRE = regexp.MustCompile(`^[^\x00-\x1f\x7f\\*?\[\]{}]+$`)
)

// maxMiseToolNameLen and maxMiseToolVersionLen bound an image.tools key and
// value: generous for any real mise tool or version, but small enough that
// neither the rendered Dockerfile nor the generated mise config can ever
// receive an oversized value.
const (
	maxMiseToolNameLen    = 128
	maxMiseToolVersionLen = 64
	// maxImageKeyFilePath bounds one checkout path — an image.key_files entry
	// before the removal, a catalog entry's own path and a patch path pnpm's
	// manifest names now — so a list of them cannot be made large from a
	// config.
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

// MiseTools is image.tools: a mise tool name to its version. A removed key's
// field (D1195), decoded as it always was so its value is there for the
// refusal to read — no stricter than a plain map, because a value this parser
// refused on its own would hide the removal sentence behind a message about
// YAML quoting.
type MiseTools map[string]string

// validateImage checks a workflow's image settings, which after the
// repository-image removal are the two 0.7.0 tombstones and the catalog
// reference below: every other key of the block is a removed key, and
// removedWorkflowProblems has refused the file before this runs — the
// grammar and length checks those keys used to have went with the code that
// honored them (the renderer, the Dockerfile and the rebuild key). p is the
// workflow's problem path, such as "workflows.web".
func validateImage(p string, w Workflow) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	img := w.Image
	if img.Node != "" {
		add(p+".image.node", "was removed in 0.7.0: declare node in mise.toml ([tools] node = %q) (%s)", pluginwire.Printable(img.Node), MigrationDoc)
	}
	if img.JDK != "" {
		add(p+".image.jdk", "was removed in 0.7.0: declare Java in mise.toml ([tools] java = %q) (%s)", "temurin-"+pluginwire.Printable(img.JDK), MigrationDoc)
	}
	ps = append(ps, validateCatalogRef(p, w)...)
	return ps
}

// RemovedImageKeyDoc is where a person who meets a key the repository-image
// removal took out of a workflow is sent: the catalog is what a repository
// builds an image with now.
const RemovedImageKeyDoc = "docs/project-image-catalog/README.md"

// RemovedImageKeyMessage is removedImageRefusal's sentence for key, one of
// RemovedImageKeys' paths ("" for any other, and for a key that is not one of
// them). The update stage that rewrites a published project layer still holding
// one says what it took out in the words the key is refused with, so a reader
// who meets the migration and a writer who meets the refusal are sent to the
// same command.
func RemovedImageKeyMessage(key string) string { return removedImageRefusal(key) }

// removedBuildKeyMessage and removedRebuildKeyMessage are the two sentences the
// removal's refusals are made of. They are templates the caller fills with
// RemovedImageKeyDoc, the shape legacyBaseMessage already has, so the guide a
// person is sent to is named in one place.
//
// Both name `fugaro image add`, because that is the command that replaces the
// key: a person who meets the refusal has to be told what to do next, and a
// decoder's "field not found" tells them nothing (D1195).
//
// Neither names the release the removal lands in. The slot the plan recommends
// is not the release that carries it — 0.20.0, 0.20.1, 0.20.2 and 0.20.3 are
// all out without it — so a number here would tell an operator their file broke
// in a release it did not break in, and the release this one does land in is the
// owner's to pick rather than this sentence's to guess. What a reader needs is
// that the key is gone and which command replaces it, and both are said.
const (
	removedBuildKeyMessage   = "was removed: a job's image is a catalog entry now, and a repository adds one with `fugaro image add` and selects it with image_name (%s)"
	removedRebuildKeyMessage = "was removed: nothing rebuilds a repository's own image any more; `fugaro image add` records a catalog entry and `fugaro image build` builds it (%s)"
)

// removedImageKey is one key the repository-image removal took out of a
// workflow, and how a workflow says that it wrote it.
//
// The write test is a value test and not a name test because the key has to
// stay decodable for its refusal to be this sentence rather than the strict
// decoder's "field not found in type config.Workflow" (D1195). It reads a
// value that applyDefaults fills in — rebuild.max_age's Set bit, rebuild's two
// pointers, rebuild.paths' nil — so removedImageKeyProblems MUST be called on a
// config that has not been defaulted; every caller below is.
type removedImageKey struct {
	key    string
	writes func(Workflow) bool
}

// removedImageKeys is the removal's own list, in the order a reader meets the
// keys in fugaro.yaml. One list, because three surfaces have to agree about it:
// the refusal below, RemovedImageKeys (which internal/settings' completeness
// guard reads, so a removed key is not forced back into the registry as a
// writable row), and this folder's own pin.
var removedImageKeys = []removedImageKey{
	{"image.apt", func(w Workflow) bool { return len(w.Image.Apt) > 0 }},
	{"image.setup", func(w Workflow) bool { return len(w.Image.Setup) > 0 }},
	{"image.skip_build_scripts", func(w Workflow) bool { return w.Image.SkipBuildScripts }},
	{"image.tools", func(w Workflow) bool { return len(w.Image.Tools) > 0 }},
	{"image.key_files", func(w Workflow) bool { return len(w.Image.KeyFiles) > 0 }},
	{"dockerfile", func(w Workflow) bool { return w.Dockerfile != "" }},
	{"rebuild.check", func(w Workflow) bool { return w.Rebuild.Check != "" }},
	{"rebuild.max_age", func(w Workflow) bool { return w.Rebuild.MaxAge.Set }},
	{"rebuild.lockfiles", func(w Workflow) bool { return w.Rebuild.Lockfiles != nil }},
	{"rebuild.base", func(w Workflow) bool { return w.Rebuild.Base != nil }},
	{"rebuild.paths", func(w Workflow) bool { return w.Rebuild.Paths != nil }},
}

// RemovedImageKeys is the removal's list as fugaro.yaml paths under
// workflows.*: the keys no surface may offer as a setting. internal/settings
// reads it to keep its registry — the table `fugaro settings set` and
// `fugaro settings list` are built from — free of a key the config refuses,
// while the struct fields the refusal needs stay in place (D1195, D1196).
func RemovedImageKeys() []string {
	out := make([]string, 0, len(removedImageKeys))
	for _, k := range removedImageKeys {
		out = append(out, "workflows.*."+k.key)
	}
	return out
}

// removedImageRefusal is the sentence refusing path, one of the removed keys
// as a workflows.* path, or "" when path is not one of them. The project
// layer's scope walk reads it (scope.go), so the two doors a workflow is
// written through refuse a removed key in the same words.
func removedImageRefusal(path string) string {
	bare, ok := strings.CutPrefix(path, "workflows.*.")
	if !ok {
		return ""
	}
	for _, k := range removedImageKeys {
		if k.key != bare {
			continue
		}
		msg := removedBuildKeyMessage
		if strings.HasPrefix(bare, "rebuild.") {
			msg = removedRebuildKeyMessage
		}
		return fmt.Sprintf(msg, RemovedImageKeyDoc)
	}
	return ""
}

// removedImageBlock reports whether path, a workflows.* path, is a block whose
// listed keys were all removed ("workflows.*.rebuild"). The scope table no
// longer makes it a block (its rows are gone), so without this the project
// layer would refuse the mapping as an unknown key instead of reaching its
// leaves and refusing each one by name.
func removedImageBlock(path string) bool {
	for _, k := range removedImageKeys {
		if strings.HasPrefix("workflows.*."+k.key, path+".") {
			return true
		}
	}
	return false
}

// removedImageKeyProblems reports every removed key p's workflow writes, each
// under its own path, each with the sentence that names the command replacing
// it. It is called before a config's defaults are filled in, for the reason
// removedImageKey states: after applyDefaults every workflow writes every
// rebuild key.
func removedImageKeyProblems(p string, w Workflow) []Problem {
	var ps []Problem
	for _, k := range removedImageKeys {
		if !k.writes(w) {
			continue
		}
		ps = append(ps, Problem{Path: p + "." + k.key, Message: removedImageRefusal("workflows.*." + k.key)})
	}
	return ps
}

// removedWorkflowProblems is removedImageKeyProblems for every workflow of a
// decoded fugaro.yaml, in the table's own order.
func removedWorkflowProblems(c *Config) []Problem {
	var ps []Problem
	for _, name := range sortedKeys(c.Workflows) {
		ps = append(ps, removedImageKeyProblems("workflows."+showKey(name), c.Workflows[name])...)
	}
	return ps
}

// validateCatalogRef checks a workflow's catalog reference (spec
// project-image-catalog §Repo configuration, D1155): image_name: names one
// entry of the project's catalog and needs: lists the tags the job needs.
// Both are the repository's, and both are definitions of what the job runs,
// which is why they are judged here rather than with the project's catalog:
// whether the name exists is the selection's question (PIC5), and what this
// refuses is a file that states two definitions at once.
//
// D1155 (answered 2026-11-02) refused a file that set its own image: or
// dockerfile: AND a catalog reference, naming both keys. Both of those keys
// are removed keys now, each refused by its own sentence before this runs, so
// there is no override left for the conflict to be between: the refusal went
// with the keys, and what stays is the reference's own grammar.
//
// A repository that names both an entry and tags is not a conflict: the
// precedence is stated (image_name: first, then needs:), so the pin wins and
// the tags are what the deploy-time warning of D1146 checks the pin against.
func validateCatalogRef(p string, w Workflow) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if w.ImageName == "" && len(w.Needs) == 0 {
		return nil
	}
	if !catalogNameRE.MatchString(w.ImageName) && w.ImageName != "" {
		add(p+".image_name", "%s", catalogNameMessage)
	}
	validateCatalogTags(p+".needs", w.Needs, add)
	return ps
}

// keyFilePathRules are the rules a path the build checks out early must obey,
// each with the message that names it. ValidKeyFilePath is their one reader
// now that image.key_files is a removed key: the catalog's own checkout list
// and the patch files pnpm's manifest names are still checked out the same
// way a hand-written entry was, so they are judged by one statement of the
// rules and not a second copy of them.
var keyFilePathRules = []func(f string) bool{
	func(f string) bool { return strings.TrimSpace(f) != "" },
	utf8.ValidString,
	func(f string) bool { return utf8.RuneCountInString(f) <= maxImageKeyFilePath },
	imageKeyFileRE.MatchString,
	func(f string) bool { return !strings.HasPrefix(f, "/") },
	func(f string) bool { return !slices.Contains(strings.Split(f, "/"), "..") },
	func(f string) bool { return path.Clean(f) == f },
	func(f string) bool { return f != "." },
	func(f string) bool { return f != ".git" && !strings.HasPrefix(f, ".git/") },
}

// ValidKeyFilePath reports whether f is a path the build may check out early:
// one clean repository-relative path, inside the repository, as the path it
// is, holding no glob, no backslash and no control character.
func ValidKeyFilePath(f string) bool {
	for _, ok := range keyFilePathRules {
		if !ok(f) {
			return false
		}
	}
	return true
}

// SharedCheckoutSince records the release shared dependency-layer images
// were to arrive in, and it is stale: 0.15.0 shipped without them, so no
// release builds or refreshes one and nothing gates on this number. The
// gate is sharedCheckoutShipped below, a flag the release that actually
// builds these images flips. The value stays at 0.15.0 because one reader
// still names it — docs/release.md's bullet for internal/cli's sharedSince,
// which TestSharedSinceHasAReleaseBullet holds to it — so the release that
// ships them moves this to its own tag.
// SharedCheckoutRefusal names no release at all, so a stale number here
// cannot reach a repository.
//
// It is a different fact from internal/cli's sharedSince, which names the
// release whose *runner* clones for a shared job (0.14.0): the runner half
// landed first, and the image such a job runs does not exist yet.
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
//
// It names no release. It used to promise "shared images arrive in 0.15.0",
// and that release shipped without them, so a version here is a promise
// either already broken or made about a slot nobody has scheduled; what
// lifts the refusal is sharedCheckoutShipped, not a number — so the sentence
// says what is true today and names the two kinds that work in it.
// TestSharedCheckoutRefusalNamesNoReleaseAsAFutureArrival holds it that way.
const SharedCheckoutRefusal = "no released fugaro builds or refreshes a shared dependency-layer image yet, so a shared workflow's job could not run; use checkout: baked to build a per-repository image, or checkout: clone to run the base image and clone the source at launch"

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
// The kind's own reading of the design — that a shared image holds the
// dependency layer "and only it" (§12), so a step that runs in the
// repository's checkout cannot belong to it — cost a workflow moving to
// shared its image.setup line (§2.2, §4, §10). That key is removed now, so
// the reading holds by construction and the refusal that stated it is gone.
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
		// The keys that used to bake this repository's own content into the
		// shared image — image.setup, dockerfile — are removed keys, refused
		// with their own sentences, so what is left to refuse here is the
		// catalog reference below.
		// A catalog reference is the same kind of key: the shared image is
		// chosen from the resolved dependency set, not from the project's
		// catalog, so a workflow that names an entry here would get an image
		// it did not ask for (D1142: the catalog is what a baked image is
		// built from, and this kind is not baked).
		if w.ImageName != "" {
			add(p+".image_name", "%s", msg)
		}
		if len(w.Needs) > 0 {
			add(p+".needs", "%s", msg)
		}
		return ps
	}
	if w.Checkout != CheckoutClone {
		return ps
	}
	const msg = "checkout: clone runs the base image with no build; use checkout: baked to build an image"
	// The keys a build would honor — image.apt, image.setup, image.tools,
	// image.skip_build_scripts, image.key_files, dockerfile — are removed
	// keys, each refused with its own sentence, so a clone workflow cannot
	// carry one any more and this arm has only the catalog reference left.
	// A catalog reference is refused for the same reason: it replaces the
	// per-workflow definition a build honors, and a clone workflow has no
	// build (D1142).
	if w.ImageName != "" {
		add(p+".image_name", "%s", msg)
	}
	if len(w.Needs) > 0 {
		add(p+".needs", "%s", msg)
	}
	return ps
}
