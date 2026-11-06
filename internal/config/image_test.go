package config

import (
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const webYAML = `
version: 1
project: aurora
git:
  provider: github
workflows:
  web:
    commands:
      build: npm run build
      test: npm test
`

// imageFieldReasons is config.Image's fields that never run as shell and so
// stay off ExecutableKeys, with why: Node and JDK were removed in 0.7.0 and
// the other five in 0.20.0, and every one of them is refused unconditionally,
// so none reaches a Dockerfile.
var imageFieldReasons = map[string]string{
	"Node":             "removed in 0.7.0; refused unconditionally by validateImage, never run as shell",
	"JDK":              "removed in 0.7.0; refused unconditionally by validateImage, never reaches a Dockerfile",
	"Apt":              "removed in 0.20.0; refused unconditionally by removedImageKeyProblems, never a command",
	"Setup":            "removed in 0.20.0; refused unconditionally by removedImageKeyProblems, never a command",
	"Tools":            "removed in 0.20.0; refused unconditionally by removedImageKeyProblems, never a command",
	"SkipBuildScripts": "removed in 0.20.0; refused unconditionally by removedImageKeyProblems, never a command",
	"KeyFiles":         "removed in 0.20.0; refused unconditionally by removedImageKeyProblems, never a command",
}

// TestImageFieldsAreClassified is decision L7's reflection-style guard: a
// field added to config.Image later must either run as shell (and so be
// gated, an entry of ExecutableKeys, named "workflows.*.image.<yaml tag>")
// or have a reason in imageFieldReasons that it is not. A field with
// neither is caught here, in CI, instead of landing ungated on a live
// publish.
func TestImageFieldsAreClassified(t *testing.T) {
	typ := reflect.TypeOf(Image{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		key := "workflows.*.image." + tag
		executable := slices.Contains(ExecutableKeys, key)
		_, reasoned := imageFieldReasons[f.Name]
		switch {
		case executable && reasoned:
			t.Errorf("Image.%s (%s): is both in ExecutableKeys and in imageFieldReasons; it cannot be both gated and exempt", f.Name, key)
		case !executable && !reasoned:
			t.Errorf("Image.%s (%s): is in neither ExecutableKeys nor imageFieldReasons; classify it as executable (gated) or add a reason it is not", f.Name, key)
		}
	}
}

// TestCatalogReferenceAloneIsAccepted is what is left of D1155 (answered
// 2026-11-02). Its refusal was for a file that set both a catalog reference
// and the repository's own image definition, image: or dockerfile:, naming
// both keys; both of those keys are removed keys now, each refused with its
// own sentence before this check runs, so a reference is the only definition
// left to write and there is nothing for it to conflict with. What still
// holds is the other half: a reference on its own is the repository naming
// the project's entry, which is the point of the key.
func TestCatalogReferenceAloneIsAccepted(t *testing.T) {
	for _, tc := range []struct{ name, extra string }{
		{"image_name alone", "    image_name: backend\n"},
		{"needs alone", "    needs: [node]\n"},
		{"both, the pin winning over the tags", "    image_name: backend\n    needs: [node]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, problems := Parse([]byte(webYAML + tc.extra)); len(problems) > 0 {
				t.Fatalf("unexpected problems: %v", problems)
			}
		})
	}
}

// TestCatalogReferenceWithCloneCheckoutIsRefused is D1142's first half:
// checkout: clone runs the installation's base image with no build, so it
// refuses a catalog reference exactly as it refuses image: and dockerfile:
// today. The reference replaces the per-workflow definition, not the
// checkout kind.
func TestCatalogReferenceWithCloneCheckoutIsRefused(t *testing.T) {
	const msg = "checkout: clone runs the base image with no build; use checkout: baked to build an image"
	for _, tc := range []struct{ name, extra, path string }{
		{"image_name", "    image_name: backend\n", "workflows.web.image_name"},
		{"needs", "    needs: [node]\n", "workflows.web.needs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Parse([]byte(cloneYAML + "    checkout: clone\n" + tc.extra))
			if !hasProblem(problems, tc.path, msg, 0) {
				t.Errorf("problems = %v, want one at %s: %s", problems, tc.path, msg)
			}
		})
	}
}

// TestCatalogReferenceWithSharedCheckoutIsRefused is D1142's second half:
// checkout: shared keeps its own refusal and this work does not lift it, so
// a catalog reference is one more key it refuses (F6). The rules the kind
// applies are reachable only behind the gate, so the test turns it on for
// the arm, and pins the gate's own refusal for the shape a repository meets
// today.
func TestCatalogReferenceWithSharedCheckoutIsRefused(t *testing.T) {
	const msg = "checkout: shared runs an image shared by every repository on the same dependency set, which holds no repository's own content; use checkout: baked to build a per-repository image"
	_, problems := Parse([]byte(cloneYAML + "    checkout: shared\n    needs: [node]\n"))
	if !hasProblem(problems, "workflows.web.checkout", SharedCheckoutRefusal, 0) {
		t.Errorf("problems = %v, want the shared refusal the gate stands for", problems)
	}
	sharedShipped(t)
	for _, tc := range []struct{ name, extra, path string }{
		{"image_name", "    image_name: backend\n", "workflows.web.image_name"},
		{"needs", "    needs: [node]\n", "workflows.web.needs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Parse([]byte(cloneYAML + "    checkout: shared\n" + tc.extra))
			if !hasProblem(problems, tc.path, msg, 0) {
				t.Errorf("problems = %v, want one at %s: %s", problems, tc.path, msg)
			}
		})
	}
}

// TestCheckoutDefaultsToBaked pins that a workflow that names no checkout:
// is baked, the kind that builds its image (design generic-tool.md §2, G3).
// It is what is left of the table that pinned checkout: clone's refusal of
// each build key: those keys are removed keys now, refused with their own
// sentence wherever they are written, so a clone workflow cannot carry one.
func TestCheckoutDefaultsToBaked(t *testing.T) {
	cfg, problems := Parse([]byte(webYAML))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if cfg.Workflows["web"].Checkout != CheckoutBaked {
		t.Fatalf("default checkout = %q, want %q", cfg.Workflows["web"].Checkout, CheckoutBaked)
	}
}

// cloneYAML is webYAML without base: web-node: checkout: clone always runs
// the Fugaro base image, so combining it with a legacy base is refused
// (TestCheckoutCloneRefusesLegacyBase), and these two tests must not
// trip that rule while pinning something else.
const cloneYAML = `
version: 1
project: aurora
git:
  provider: github
workflows:
  web:
    commands:
      build: npm run build
      test: npm test
`

// TestCheckoutCloneAlone pins that checkout: clone is valid on its own: the
// kind keeps both its values (D1191) and the build keys it used to be refused
// beside are removed keys now, refused wherever they are written.
func TestCheckoutCloneAlone(t *testing.T) {
	cfg, problems := Parse([]byte(cloneYAML + "    checkout: clone\n"))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if cfg.Workflows["web"].Checkout != CheckoutClone {
		t.Fatalf("checkout = %q, want %q", cfg.Workflows["web"].Checkout, CheckoutClone)
	}
}

// TestCheckoutCloneAllowsEmptyBuildKeys pins the shape of the removal's
// refusals: each one is a write test (removedImageKeys), so an empty
// image.apt, image.setup, image.tools or skip_build_scripts is not something
// a workflow wrote and the decoder accepts it. The key stays decodable for the
// value a workflow did write: that one is refused by name
// (TestRemovedImageKeysAreRefusedByName) rather than with the strict decoder's
// "field not found".
func TestCheckoutCloneAllowsEmptyBuildKeys(t *testing.T) {
	cfg, problems := Parse([]byte(cloneYAML + "    checkout: clone\n    image: { apt: [], setup: [], tools: {}, skip_build_scripts: false }\n"))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if cfg.Workflows["web"].Checkout != CheckoutClone {
		t.Fatalf("checkout = %q, want %q", cfg.Workflows["web"].Checkout, CheckoutClone)
	}
}

// sharedYAML is webYAML with checkout: shared. The dependency-layer keys it
// used to carry — the tools, the packages, the key files and the warm-up —
// are removed keys now, refused wherever they are written, so the shape the
// new kind makes expressible is the kind alone.
const sharedYAML = webYAML + `    checkout: shared
`

// sharedShipped turns the launch gate on for the duration of one test
// and restores it: validation refuses every checkout: shared today
// (TestValidateRefusesCheckoutSharedUntilTheImagesShip), so the rules the
// release that ships shared images will apply are reachable only behind the
// flag. The release that flips sharedCheckoutShipped makes these tests run
// it open.
func sharedShipped(t *testing.T) {
	t.Helper()
	was := sharedCheckoutShipped
	sharedCheckoutShipped = true
	t.Cleanup(func() { sharedCheckoutShipped = was })
}

// TestSharedCheckoutShippedIsTheGateValidationRefusesBy: internal/infra reads
// the exported accessor to decide whether a job whose environment carries
// FUGARO_CHECKOUT=shared may have its image moved, so it has to be the same
// flag validateCheckout refuses a config by. An accessor reading anything else
// would let the cloud side accept the mark while the release that builds the
// image has not run, or refuse it after the flip.
func TestSharedCheckoutShippedIsTheGateValidationRefusesBy(t *testing.T) {
	if SharedCheckoutShipped() {
		t.Fatal("this build ships shared images; this test pins the gate while it is off and moves with the flip")
	}
	if _, problems := Parse([]byte(sharedYAML)); !hasProblem(problems, "workflows.web.checkout", SharedCheckoutRefusal, 0) {
		t.Fatalf("problems = %v, want the refusal the gate names", problems)
	}
	sharedShipped(t)
	if !SharedCheckoutShipped() {
		t.Error("the accessor did not follow the flag")
	}
	if _, problems := Parse([]byte(sharedYAML)); len(problems) > 0 {
		t.Fatalf("problems = %v, want the gated rules to accept it", problems)
	}
}

// sharedReleaseRE finds a release number in a message, in the shape the
// *Since constants are written in, so the test below sees one spliced back
// into the refusal.
var sharedReleaseRE = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)

// TestSharedCheckoutRefusalNamesNoReleaseAsAFutureArrival: the refusal said
// shared images arrive in 0.15.0, and 0.15.0 shipped without one — no
// released fugaro builds or refreshes a shared dependency-layer image
// (C8b1b/C8b1c are unreleased). Every number it could carry is wrong the
// same way: the release it names is either out and without the images, or a
// promise about a slot nobody has scheduled. The gate the refusal stands
// for is sharedCheckoutShipped, a flag the release that builds these images
// flips, and what the reader is owed is the state of the world today and
// the two kinds that work in it, in one sentence.
func TestSharedCheckoutRefusalNamesNoReleaseAsAFutureArrival(t *testing.T) {
	if v := sharedReleaseRE.FindString(SharedCheckoutRefusal); v != "" {
		t.Errorf("the refusal names %q as the release shared images arrive in: no released fugaro builds or refreshes one yet, so it can name no release at all", v)
	}
	if strings.Contains(SharedCheckoutRefusal, ". ") || strings.HasSuffix(SharedCheckoutRefusal, ".") {
		t.Errorf("the refusal reads as more than one sentence: %q", SharedCheckoutRefusal)
	}
}

// TestValidateRefusesCheckoutSharedUntilTheImagesShip: no released fugaro
// builds or refreshes a shared dependency-layer image, so a repository that
// declares checkout: shared would get a job that cannot run. Validation
// refuses the key where the repository typed it, names the two kinds that
// work today, and names no release as the one that lifts the refusal —
// including for a workflow that also declares the dependency-layer keys,
// which are the ones only this kind accepts and so the ones a reader
// reaches for first.
func TestValidateRefusesCheckoutSharedUntilTheImagesShip(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"bare", webYAML + "    checkout: shared\n"},
		{"with a catalog reference", webYAML + "    checkout: shared\n    image_name: backend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Parse([]byte(tc.yaml))
			if !hasProblem(problems, "workflows.web.checkout", SharedCheckoutRefusal, 0) {
				t.Fatalf("problems = %v, want the refusal at workflows.web.checkout", problems)
			}
			for _, want := range []string{"checkout: baked", "checkout: clone"} {
				if p := problems[0]; !strings.Contains(p.Message, want) {
					t.Errorf("the refusal %q does not name %q", p.Message, want)
				}
			}
		})
	}
}

// TestCheckoutInvalidValue pins that checkout: is an enum of baked, clone
// and shared.
func TestCheckoutInvalidValue(t *testing.T) {
	_, problems := Parse([]byte(webYAML + "    checkout: sometimes\n"))
	if !hasProblem(problems, "workflows.web.checkout", "must be baked or clone or shared; shared runs a shared dependency-layer image", 0) {
		t.Fatalf("problems = %v", problems)
	}
}

// TestCheckoutBadValueCorpus is testdata/config/invalid/checkout-bad-value.yaml
// (the schema corpus: fugaro.schema.json's checkout enum had no corpus case),
// read from disk like TestGoBase's and TestJavaServicesBase's corpus cases:
// it must fail for exactly the one reason the file exists to pin.
func TestCheckoutBadValueCorpus(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/invalid/checkout-bad-value.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, ps := Parse(data); len(ps) != 1 || ps[0].Path != "workflows.web.checkout" || !strings.Contains(ps[0].Message, "must be baked or clone") {
		t.Fatalf("problems = %v, want exactly one at workflows.web.checkout: must be baked or clone", ps)
	}
}

// TestCheckoutCloneToolsCorpus is testdata/config/invalid/checkout-clone-tools.yaml,
// read from disk like TestCheckoutBadValueCorpus: the fixture is invalid for
// exactly one reason, which is now the removal's own sentence at the key the
// fixture carries rather than the clone refusal the key used to meet. The
// fixture stays in the corpus because what an invalid fixture pins is that
// the tree refuses it for one stated reason (and, on the schema side, that
// fugaro.schema.json refuses it too).
func TestCheckoutCloneToolsCorpus(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/invalid/checkout-clone-tools.yaml")
	if err != nil {
		t.Fatal(err)
	}
	const msg = "was removed in 0.20.0"
	if _, ps := Parse(data); len(ps) != 1 || ps[0].Path != "workflows.web.image.tools" || !strings.Contains(ps[0].Message, msg) {
		t.Fatalf("problems = %v, want exactly one at workflows.web.image.tools: %s", ps, msg)
	}
}

// TestCheckoutCloneSkipBuildCorpus is
// testdata/config/invalid/checkout-clone-skip-build.yaml, read from disk like
// TestCheckoutCloneToolsCorpus: one reason, the removal's sentence at
// image.skip_build_scripts.
func TestCheckoutCloneSkipBuildCorpus(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/invalid/checkout-clone-skip-build.yaml")
	if err != nil {
		t.Fatal(err)
	}
	const msg = "was removed in 0.20.0"
	if _, ps := Parse(data); len(ps) != 1 || ps[0].Path != "workflows.web.image.skip_build_scripts" || !strings.Contains(ps[0].Message, msg) {
		t.Fatalf("problems = %v, want exactly one at workflows.web.image.skip_build_scripts: %s", ps, msg)
	}
}

// TestCheckoutCloneRefusesLegacyBase (review finding 4, LOW): checkout:
// clone always runs the Fugaro base image, so a base: alongside it is
// refused rather than silently ignored. Since the 0.7.0 cut every base:
// (the legacy go, java-services, web-node, and base itself) is refused by
// the shared legacyBaseMessage; this pins that the refusal still holds with
// checkout: clone, and that it is exactly one problem (not doubled by a
// clone-specific copy).
//
// Mutation (run, restore): remove the `if w.Base != ""` refusal from
// validate.go's workflow loop — this test then wrongly accepts every base:
// with checkout: clone.
func TestCheckoutCloneRefusesLegacyBase(t *testing.T) {
	for _, base := range []string{"go", "java-services", "web-node", "base"} {
		yaml := "\nversion: 1\nproject: aurora\ngit:\n  provider: github\nworkflows:\n  web:\n    base: " + base +
			"\n    checkout: clone\n    commands:\n      build: x\n      test: x\n"
		_, ps := Parse([]byte(yaml))
		if len(ps) != 1 || ps[0].Path != "workflows.web.base" || !strings.Contains(ps[0].Message, "was removed in 0.7.0") {
			t.Errorf("base %s: problems = %v, want exactly one at workflows.web.base: was removed in 0.7.0", base, ps)
		}
	}
	// No base: at all is the clone shape.
	cfg, problems := Parse([]byte(cloneYAML + "    checkout: clone\n"))
	if len(problems) > 0 {
		t.Fatalf("checkout: clone with no base: unexpected problems: %v", problems)
	}
	if cfg.Workflows["web"].Checkout != CheckoutClone {
		t.Fatalf("checkout = %q, want %q", cfg.Workflows["web"].Checkout, CheckoutClone)
	}
}

// TestCheckoutCloneLegacyBaseCorpus is
// testdata/config/invalid/checkout-clone-legacy-base.yaml, read from disk
// like TestCheckoutCloneSkipBuildCorpus: exactly one problem, the cut's
// base: refusal.
func TestCheckoutCloneLegacyBaseCorpus(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/invalid/checkout-clone-legacy-base.yaml")
	if err != nil {
		t.Fatal(err)
	}
	const msg = "was removed in 0.7.0"
	if _, ps := Parse(data); len(ps) != 1 || ps[0].Path != "workflows.web.base" || !strings.Contains(ps[0].Message, msg) {
		t.Fatalf("problems = %v, want exactly one at workflows.web.base: %s", ps, msg)
	}
}

// TestValidMiseTool is a table test of the single gate validateImage uses
// for every image.tools entry. Every image.tools key or version reaches a
// Dockerfile or a generated mise config verbatim (design base-image.md
// section 4), so this charset is a strict allowlist, not a denylist: each
// bad case below is a valid prefix plus exactly one character (or, for the
// length caps, one byte) that must not be let through. A regex or cap
// change that lets any of these pass is a real security regression
// (arbitrary code execution through a mise backend such as cargo:, go:,
// npm:, asdf:/vfox: or ubi:/github:/aqua:, per the ExecutableKeys ruling
// above).
func TestValidMiseTool(t *testing.T) {
	for _, name := range []string{"npm:@scope/pkg", "aqua:owner/repo", "node"} {
		if !ValidMiseTool(name, "1.0.0") {
			t.Errorf("ValidMiseTool(%q, \"1.0.0\") = false, want true", name)
		}
	}
	for _, version := range []string{"temurin-25", "24.19.0"} {
		if !ValidMiseTool("node", version) {
			t.Errorf("ValidMiseTool(\"node\", %q) = false, want true", version)
		}
	}
	badVersions := []string{
		"24\"", "24'", "24\\", "24\n", "24 ", "24;", "24$x", "24`x`",
		"24/x", "24@x", "24%", "24\x00", "２４", "-24",
	}
	for _, v := range badVersions {
		if ValidMiseTool("node", v) {
			t.Errorf("ValidMiseTool(\"node\", %q) = true, want false", v)
		}
	}
	badNames := []string{
		`node"`, "node\n", "node x", "node=", "node]", "!node", "Node", "-node", "ｎode",
	}
	for _, name := range badNames {
		if ValidMiseTool(name, "1.0.0") {
			t.Errorf("ValidMiseTool(%q, \"1.0.0\") = true, want false", name)
		}
	}
}

// TestValidMiseToolLengthCaps pins the exact boundary of each cap: the
// longest accepted key or version, and one byte past it.
func TestValidMiseToolLengthCaps(t *testing.T) {
	longName := strings.Repeat("a", maxMiseToolNameLen)
	if !ValidMiseTool(longName, "1.0.0") {
		t.Errorf("a %d-byte name must be valid", maxMiseToolNameLen)
	}
	if ValidMiseTool(longName+"a", "1.0.0") {
		t.Errorf("a %d-byte name must be refused", maxMiseToolNameLen+1)
	}
	longVersion := strings.Repeat("1", maxMiseToolVersionLen)
	if !ValidMiseTool("node", longVersion) {
		t.Errorf("a %d-byte version must be valid", maxMiseToolVersionLen)
	}
	if ValidMiseTool("node", longVersion+"1") {
		t.Errorf("a %d-byte version must be refused", maxMiseToolVersionLen+1)
	}
}

// TestServicesCorpusGetsUniformDefaults: testdata/config/valid/services.yaml
// (the java-services replacement, design base-image.md §5) gets the uniform
// defaults (B10), not the old java-services base's 16Gi.
func TestServicesCorpusGetsUniformDefaults(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/valid/services.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, problems := Parse(data)
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	w := cfg.Workflows["server"]
	if w.Resources.CPU != 4 || w.Resources.Memory != "8Gi" ||
		!slices.Equal(w.Commands.Reports, []string{"**/junit*.xml", "**/build/test-results/**/*.xml"}) {
		t.Errorf("defaults = %+v, reports %v", w.Resources, w.Commands.Reports)
	}
}

// TestValidKeyFilePath is the grammar a path the build checks out early must
// obey, in one table. It was image.key_files' grammar, reported entry by
// entry with the message that named the rule; that key is removed, and its
// two readers now are the catalog's own checkout list and the patch files a
// pnpm manifest names, both judged by ValidKeyFilePath — one statement of
// the rules rather than a second copy of them. Every entry below is a path
// that would name something other than one file inside the repository (a
// glob the checkout would read as a pattern, a backslash git reads as an
// escape in a pathspec, a control character that would reach a generated
// Dockerfile line) or a path that is simply a path.
func TestValidKeyFilePath(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry string
		ok    bool
	}{
		{"a directory", "patches", true},
		{"a file in a directory", ".yarn/patches/left-pad.patch", true},
		{"a space", "vendor/my tool", true},
		{"a non-ASCII path", "vendor/café/tool", true},
		{"at the length cap", strings.Repeat("a", maxImageKeyFilePath), true},
		{"over the length cap", strings.Repeat("a", maxImageKeyFilePath+1), false},
		{"absolute", "/etc/passwd", false},
		{"a parent segment", "../outside", false},
		{"a parent segment in the middle", "a/../../etc/passwd", false},
		{"a bare parent", "..", false},
		{".git/config", ".git/config", false},
		{"the .git directory", ".git", false},
		{"the repository root", ".", false},
		{"a trailing slash", "patches/", false},
		{"a doubled slash", "a//b", false},
		{"a dot segment", "./patches", false},
		{"a dot segment in the middle", "a/./b", false},
		{"empty", "", false},
		{"only spaces", "   ", false},
		{"a backslash", `a\b`, false},
		{"an escape sequence", "a\x1bb", false},
		{"a newline", "a\nb", false},
		{"a star", "*.patch", false},
		{"a doublestar", "**/*.patch", false},
		{"a character class", "p[abc]", false},
		{"a brace group", "p{a,b}", false},
		{"a question mark", "patch?", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidKeyFilePath(tc.entry); got != tc.ok {
				t.Errorf("ValidKeyFilePath(%q) = %v, want %v", tc.entry, got, tc.ok)
			}
		})
	}
}
