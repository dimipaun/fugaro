package scripts_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
	"gopkg.in/yaml.v3"
)

// noticesScript generates the licence and notice texts of every Go module the
// released binary links; .goreleaser.yaml's before hooks run it so the file is
// in the tree when the archive is built (licensing plan L2.1).
const noticesScript = "third-party-notices.sh"

// noticesFile is the path the script writes the notices to and the name the
// archive carries them under, beside LICENSE and README.md.
const noticesFile = "THIRD_PARTY_NOTICES.md"

// moduleLine is the go list template the script and these tests both read the
// module set with: one line per module, "path<TAB>version<TAB>directory", and
// nothing at all for the main module, which carries no version.
const moduleLine = `{{if .Module}}{{if .Module.Version}}{{.Module.Path}}{{"\t"}}{{.Module.Version}}{{"\t"}}{{.Module.Dir}}{{end}}{{end}}`

// noticesTargets is the build matrix .goreleaser.yaml builds, which the script
// asks go list -deps once per pair: a module can be linked for one GOOS/GOARCH
// pair only, so the file is their union. TestGoreleaserReleaseBuild holds this
// list to the config's own goos/goarch.
var noticesTargets = []struct{ goos, goarch string }{
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
}

// TestThirdPartyNoticesCoverEveryLinkedModule: the file the archive ships names
// every module `go list -deps ./cmd/fugaro` reaches for each target of the
// build matrix, with the text of every LICENSE, COPYING and NOTICE file that
// module's directory holds. A module whose directory carries no licence file
// fails rather than passing quietly: the archive would then ship that module's
// code with no notice at all, which is the gap this task closes.
func TestThirdPartyNoticesCoverEveryLinkedModule(t *testing.T) {
	root := testutil.ModuleRoot()
	out := filepath.Join(t.TempDir(), noticesFile)
	runNoticesScript(t, root, out, nil)

	notices := readText(t, out)
	if !strings.HasPrefix(notices, "# ") {
		t.Fatalf("%s does not open with a heading: it is not the notices file\n%s", out, firstLines(notices, 5))
	}

	modules := linkedModules(t, root)
	if len(modules) < 20 {
		t.Fatalf("go list -deps named %d modules for ./cmd/fugaro; the enumeration in this test is broken, not the script", len(modules))
	}
	noticesFiles := 0
	for _, key := range slices.Sorted(maps.Keys(modules)) {
		section := noticesSection(notices, key)
		if section == "" {
			t.Errorf("%s is linked into the binary but has no section in %s", key, noticesFile)
			continue
		}
		files := licenceFiles(t, modules[key])
		if len(files) == 0 {
			t.Errorf("%s ships no LICENSE, COPYING or NOTICE file in its module directory, so %s carries no notice for it; a person has to look at that module", key, noticesFile)
			continue
		}
		for _, name := range files {
			body := strings.TrimSpace(readText(t, filepath.Join(modules[key], name)))
			if body == "" {
				t.Errorf("%s is empty, so nothing can carry %s's notice", filepath.Join(modules[key], name), key)
				continue
			}
			if !strings.Contains(section, body) {
				t.Errorf("the section for %s does not carry %s's text", key, name)
			}
			if strings.Contains(strings.ToUpper(name), "NOTICE") {
				noticesFiles++
			}
		}
	}
	if noticesFiles == 0 {
		t.Errorf("no linked module has a NOTICE file any more, so nothing here checks that the script carries one; re-read the script's glob before deleting this guard")
	}
}

// TestThirdPartyNoticesUnionsTheBuildMatrix: the script asks go list -deps once
// per target and with the release build's own tags, so a module one target links
// and another does not -- or one only the tagged build links -- still gets a
// section. A fake `go` on PATH answers with a module set that depends on
// GOOS/GOARCH and on the tags, and it refuses every call that is not `go list`,
// so the test also pins the one thing the script may ask the toolchain for.
func TestThirdPartyNoticesUnionsTheBuildMatrix(t *testing.T) {
	root := testutil.ModuleRoot()
	bin := t.TempDir()
	modules := t.TempDir()
	writeModuleDir(t, modules, "everywhere", "LICENSE", "the everywhere module's licence text\n")
	writeModuleDir(t, modules, "darwinonly", "LICENSE.txt", "the darwin-only module's licence text\n")
	writeModuleDir(t, modules, "tagonly", "LICENSE", "the tag-only module's licence text\n")
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), noticesFile)
	runNoticesScript(t, root, out, []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_MODULE_DIR=" + modules,
	})

	want := []string{
		"example.com/everywhere", "the everywhere module's licence text",
		"example.com/darwinonly", "the darwin-only module's licence text",
	}
	// The fake answers a tagged call with a module no untagged call links, so the
	// tag-only module is expected exactly when this tree's release build compiles
	// the tag: the core-without-ee workflow deletes ee/, and there the tag names
	// no code for the script to ask for.
	if len(releaseTags(t, root)) > 0 {
		want = append(want, "example.com/tagonly", "the tag-only module's licence text")
	}

	notices := readText(t, out)
	for _, want := range want {
		if !strings.Contains(notices, want) {
			t.Errorf("%s lacks %q: the darwin/arm64 target's modules, or the tagged build's, are missing, so the script does not ask go list -deps once per target and with the release build's tags\n%s", noticesFile, want, firstLines(notices, 20))
		}
	}
}

// TestThirdPartyNoticesFailsWhenATargetFails: the before hook is the only place
// the file is produced for real, and an archive whose notices are missing one
// target's modules is exactly the gap this task closes, so a target go list
// cannot answer for has to stop the release. The enumeration is a pipeline into
// sort, and sort's is the status a shell reports without pipefail -- so this
// holds the script to a non-zero exit and to no output file at all, the partial
// one being worse than none because the archive ships whatever is at that path.
func TestThirdPartyNoticesFailsWhenATargetFails(t *testing.T) {
	root := testutil.ModuleRoot()
	bin := t.TempDir()
	modules := t.TempDir()
	writeModuleDir(t, modules, "everywhere", "LICENSE", "the everywhere module's licence text\n")
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), noticesFile)
	cmd := noticesCommand(root, out, []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_MODULE_DIR=" + modules,
		// The first target of the build matrix, which is where a swallowed
		// failure leaves the other three's modules and no sign of the gap.
		"FAKE_FAIL_TARGET=darwin/amd64",
	})
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Errorf("the script exited 0 although go list cannot answer for darwin/amd64, so the release would ship a notices file missing that target's modules\nstderr:\n%s", stderr.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("the script wrote %s although a target's go list failed: the archive ships whatever is at that path, so a partial notices file is worse than none", out)
	}
}

// fakeGo stands in for the toolchain in TestThirdPartyNoticesUnionsTheBuildMatrix
// and TestThirdPartyNoticesFailsWhenATargetFails. The notices script may ask the
// toolchain for the module list and nothing else, so every other call fails
// loudly; the darwin/arm64 answer carries a module no other target links, and a
// tagged call carries one no untagged call links, which only a per-target,
// tagged enumeration can find; FAKE_FAIL_TARGET names a target it cannot answer
// for, which is the release-time failure the script has to stop on; and
// FAKE_ARGS_FILE makes it record the arguments it was called with, which is how
// TestGoreleaserReleaseBuild reads the tag set the script asks for.
const fakeGo = `#!/usr/bin/env bash
set -eu
if [ -n "${FAKE_ARGS_FILE:-}" ]; then
  printf '%s\n' "$*" >> "$FAKE_ARGS_FILE"
fi
if [ "${1:-}" != "list" ]; then
  echo "fake go: the notices script called 'go $*'; it may only call 'go list'" >&2
  exit 1
fi
if [ -n "${FAKE_FAIL_TARGET:-}" ] && [ "$GOOS/$GOARCH" = "$FAKE_FAIL_TARGET" ]; then
  echo "fake go: cannot list $GOOS/$GOARCH" >&2
  exit 1
fi
printf 'example.com/everywhere\tv1.0.0\t%s/everywhere\n' "$FAKE_MODULE_DIR"
if [ "$GOOS" = darwin ] && [ "$GOARCH" = arm64 ]; then
  printf 'example.com/darwinonly\tv1.0.0\t%s/darwinonly\n' "$FAKE_MODULE_DIR"
fi
case " $* " in
  *" -tags="*) printf 'example.com/tagonly\tv1.0.0\t%s/tagonly\n' "$FAKE_MODULE_DIR" ;;
esac
`

// writeModuleDir plants a module directory holding one licence file, which is
// the shape the script reads a module's notices from.
func writeModuleDir(t *testing.T, root, name, file, text string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGoreleaserArchivesShipNotices: the archive carries the notices file the
// before hook generates. The hook names the path it writes and the archive
// names the same path, so neither can move without the other, and the path is a
// bare file name: a file under a directory lands inside the archive with that
// directory around it.
func TestGoreleaserArchivesShipNotices(t *testing.T) {
	root := testutil.ModuleRoot()
	cfg := readGoreleaser(t, filepath.Join(root, ".goreleaser.yaml"))
	out := noticesHookOutput(t, cfg)
	if strings.ContainsAny(out, `/\`) {
		t.Errorf("the notices hook writes to %q; the archive would carry it under that directory, so it has to be a bare file name", out)
	}
	if len(cfg.Archives) == 0 {
		t.Fatal(".goreleaser.yaml has no archives entry, so there is no archive to carry the notices")
	}
	for _, f := range cfg.Archives[0].Files {
		if f.Src == out || f.Dst == out || f.Glob == out {
			return
		}
	}
	t.Errorf("archives[0].files does not name %q, so the archive ships no third-party notices", out)
}

// TestGoreleaserReleaseBuild: the release build is the one place a build tag
// could compile commercial code into the released binary (D303 as amended), so
// this reads the whole of what decides that: the builds, the before hooks, the
// archive's file list, and the effective tag set, which is the union of every
// route a tag can take rather than one key.
func TestGoreleaserReleaseBuild(t *testing.T) {
	root := testutil.ModuleRoot()
	cfg := readGoreleaser(t, filepath.Join(root, ".goreleaser.yaml"))
	workflow := readText(t, filepath.Join(root, ".github", "workflows", "release.yml"))

	t.Run("every build's main is ./cmd/fugaro", func(t *testing.T) {
		if len(cfg.Builds) == 0 {
			t.Fatal(".goreleaser.yaml has no builds entry")
		}
		for i, b := range cfg.Builds {
			if b.Main != "./cmd/fugaro" {
				t.Errorf("builds[%d].main is %q, not ./cmd/fugaro: the release would ship a binary other than the CLI", i, b.Main)
			}
		}
	})

	t.Run("the notices matrix is the build matrix", func(t *testing.T) {
		var want []string
		for _, b := range cfg.Builds {
			for _, goos := range b.Goos {
				for _, goarch := range b.Goarch {
					want = append(want, goos+"/"+goarch)
				}
			}
		}
		var got []string
		for _, tgt := range noticesTargets {
			got = append(got, tgt.goos+"/"+tgt.goarch)
		}
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(want, got) {
			t.Errorf("the config builds %v and these tests ask go list -deps for %v; a target the config builds and the notices script does not ask about can link a module the archive carries no notice for", want, got)
		}
	})

	t.Run("the before hooks are the two the release owes", func(t *testing.T) {
		want := []string{"go mod verify", "./scripts/" + noticesScript + " " + noticesFile}
		if !slices.Equal(cfg.Before.Hooks, want) {
			t.Errorf("before.hooks is %v, not %v: `go mod verify` first, then the notices the archive ships", cfg.Before.Hooks, want)
		}
	})

	t.Run("no archive file reaches ee/", func(t *testing.T) {
		for i, a := range cfg.Archives {
			for _, f := range a.Files {
				for _, p := range []string{f.Src, f.Dst, f.Glob} {
					if reachesEE(p) {
						t.Errorf("archives[%d].files names %q: nothing in the archive may reach the reserved ee/ directory", i, p)
					}
				}
			}
		}
	})

	t.Run("the effective tag set", func(t *testing.T) {
		tags := buildTagSet(t, cfg, []byte(workflow))
		for _, tag := range slices.Sorted(maps.Keys(tags)) {
			if tag != "ee" {
				t.Errorf("the release build sets the build tag %q; only ee may be set, and then only with every licence label naming the Subscription Edition licence (TestEETagNeedsTheLabels)", tag)
			}
		}
	})

	// The archive ships the notices of the binary it carries, so the script has
	// to ask the toolchain for the build this config declares: a module only one
	// of the two links ships with no notice at all, or with one for code the
	// archive does not carry.
	t.Run("the notices are enumerated for the build this config declares", func(t *testing.T) {
		if got, want := noticesScriptTags(t, root), releaseTags(t, root); !slices.Equal(got, want) {
			t.Errorf("scripts/%s asks go list -deps for the build tags %v and this config's builds set %v; the release workflow's own environment is inherited by the hook either way", noticesScript, got, want)
		}
	})

	t.Run("the tag reader sees every route a tag can take", func(t *testing.T) {
		for _, c := range []struct {
			name     string
			config   string
			workflow string
			want     []string
		}{
			{name: "no tag anywhere", config: "builds:\n  - main: ./cmd/fugaro\n", want: nil},
			{name: "tags on the build", config: "builds:\n  - tags: [ee]\n", want: []string{"ee"}},
			{name: "tags in the build's flags", config: "builds:\n  - flags: [-trimpath, -tags=ee]\n", want: []string{"ee"}},
			{name: "tags in the build's flags, as two elements", config: "builds:\n  - flags: [-tags, ee]\n", want: []string{"ee"}},
			{name: "GOFLAGS in the build's env", config: "builds:\n  - env: [CGO_ENABLED=0, GOFLAGS=-tags=ee]\n", want: []string{"ee"}},
			{name: "GOFLAGS in the top-level env", config: "env:\n  GOFLAGS: -tags=ee\n", want: []string{"ee"}},
			{name: "tags on an override", config: "builds:\n  - overrides:\n      - goos: linux\n        tags: [ee]\n", want: []string{"ee"}},
			{name: "tags in an override's flags", config: "builds:\n  - overrides:\n      - goos: linux\n        flags: [-tags=ee]\n", want: []string{"ee"}},
			{name: "GOFLAGS in an override's env", config: "builds:\n  - overrides:\n      - goos: linux\n        env: [GOFLAGS=-tags=ee]\n", want: []string{"ee"}},
			{name: "GOFLAGS in the release workflow's goreleaser step", workflow: workflowWithGOFLAGS(goreleaserStep, "GOFLAGS: -tags=ee"), want: []string{"ee"}},
			{name: "GOFLAGS in the release workflow's goreleaser step, as a list", workflow: workflowWithGOFLAGS(goreleaserStep, `["GOFLAGS=-tags=ee"]`), want: []string{"ee"}},
			{name: "GOFLAGS in a step that is not goreleaser's", workflow: workflowWithGOFLAGS("actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e", "GOFLAGS: -tags=ee"), want: nil},
			{name: "GOFLAGS on the job that runs goreleaser", workflow: "jobs:\n  release:\n    env:\n      GOFLAGS: -tags=ee\n    steps:\n      - uses: " + goreleaserStep + "\n", want: []string{"ee"}},
			{name: "GOFLAGS at the workflow's own level", workflow: "env:\n  GOFLAGS: -tags=ee\njobs:\n  release:\n    steps:\n      - uses: " + goreleaserStep + "\n", want: []string{"ee"}},
			{name: "GOFLAGS on a job that runs no goreleaser", workflow: "jobs:\n  check:\n    env:\n      GOFLAGS: -tags=ee\n    steps:\n      - run: echo nothing\n", want: nil},
		} {
			t.Run(c.name, func(t *testing.T) {
				cfg := parseGoreleaser(t, c.config)
				workflow := c.workflow
				if workflow == "" {
					workflow = noTagWorkflow
				}
				got := slices.Sorted(maps.Keys(buildTagSet(t, cfg, []byte(workflow))))
				if !slices.Equal(got, c.want) {
					t.Errorf("the reader sees the tag set %v, want %v", got, c.want)
				}
			})
		}
	})
}

// workflowWithGOFLAGS is a release workflow with one step, whose env block is
// written exactly as env is given, so the reader is held to both the mapping
// form and the list form the workflow files use.
func workflowWithGOFLAGS(uses, env string) string {
	return fmt.Sprintf("jobs:\n  release:\n    steps:\n      - uses: %s\n        env:\n          %s\n", uses, env)
}

// goreleaserStep is the action the release workflow runs GoReleaser through;
// only a step carrying it is the build this test reads GOFLAGS from.
const goreleaserStep = "goreleaser/goreleaser-action@a4f02ab5355a589de60d917a659ce617ed19b3de"

// noTagWorkflow is a release workflow that runs no goreleaser step, so it adds
// nothing to the effective tag set: the plant for a route that is not the
// workflow's, and the negative case for a workflow that carries a tag.
const noTagWorkflow = "jobs:\n  release:\n    steps:\n      - run: echo nothing\n"

// eeTagRoutes are the ways the release build can set the ee tag, one per route
// the tag reader knows. TestEETagNeedsTheLabels runs its plants over all of
// them: a coupling that read `tags:` alone would let a release ship the
// commercial binary with MIT labels on the other five routes.
var eeTagRoutes = []struct{ name, config, workflow string }{
	{"tags on the build", "builds:\n  - tags: [ee]\n", noTagWorkflow},
	{"-tags in the build's flags", "builds:\n  - flags: [-tags=ee]\n", noTagWorkflow},
	{"GOFLAGS in the build's env", "builds:\n  - env: [GOFLAGS=-tags=ee]\n", noTagWorkflow},
	{"GOFLAGS in the top-level env", "env:\n  GOFLAGS: -tags=ee\n", noTagWorkflow},
	{"GOFLAGS in an override's env", "builds:\n  - overrides:\n      - goos: linux\n        env: [GOFLAGS=-tags=ee]\n", noTagWorkflow},
	{"GOFLAGS in the release workflow's goreleaser step", "", workflowWithGOFLAGS(goreleaserStep, "GOFLAGS: -tags=ee")},
}

// TestEETagNeedsTheLabels: setting the ee tag in the release build compiles
// commercial code into the binary every user downloads, so from the first
// release that sets it, every licence label has to say so (D303 as amended).
// 0.11.0 sets no tag, so the real tree holds; the plants are what keep the
// coupling from being vacuous, and they are the state L5's release moves to.
// Each plant is a whole release -- a config, a workflow and a tree of labels --
// so the tag and the labels are read from the same place they are in this
// repository, and no caller holds the labels to a tag set of its own.
func TestEETagNeedsTheLabels(t *testing.T) {
	if gaps := eeTagLabelGaps(t, testutil.ModuleRoot()); len(gaps) > 0 {
		for _, gap := range gaps {
			t.Errorf("the release build sets the ee tag, so %s", gap)
		}
	}

	for _, route := range eeTagRoutes {
		t.Run("a planted ee tag with MIT labels is refused: "+route.name, func(t *testing.T) {
			root := plantRelease(t, mitLabels, "homebrew_casks:\n  - license: MIT\n"+route.config, route.workflow)
			gaps := eeTagLabelGaps(t, root)
			if len(gaps) != len(labelNames) {
				t.Fatalf("the MIT labels report %d gaps, want one for each of the %d labels: %v", len(gaps), len(labelNames), gaps)
			}
			joined := strings.Join(gaps, "\n")
			for _, name := range labelNames {
				if !strings.Contains(joined, name) {
					t.Errorf("no gap names %s: %v", name, gaps)
				}
			}
		})
	}

	t.Run("a planted ee tag with every label naming the Subscription Edition licence is accepted", func(t *testing.T) {
		for _, route := range eeTagRoutes {
			root := plantRelease(t, subscriptionLabels, "homebrew_casks:\n  - license: "+subscriptionMarker+"\n"+route.config, route.workflow)
			if gaps := eeTagLabelGaps(t, root); len(gaps) > 0 {
				t.Errorf("a tree whose every label names the Subscription Edition licence reports gaps (%s): %v", route.name, gaps)
			}
		}
	})

	for _, gap := range imageTagLabelGaps(t, testutil.ModuleRoot()) {
		t.Errorf("an image builds the tagged binary, so %s", gap)
	}
	for _, c := range imagePlants {
		t.Run(c.name, func(t *testing.T) {
			root := plantImages(t, c.bodies)
			gaps := imageTagLabelGaps(t, root)
			if !c.refused {
				if len(gaps) > 0 {
					t.Errorf("%s reports gaps: %v", c.name, gaps)
				}
				return
			}
			if len(gaps) == 0 {
				t.Fatalf("%s reports no gap, so an image that publishes the tagged binary with an MIT licence label would ship", c.name)
			}
			if !strings.Contains(strings.Join(gaps, "\n"), c.want) {
				t.Errorf("%s reports %v, and no gap names %q: a failure has to say which image to relabel", c.name, gaps, c.want)
			}
		})
	}
}

// imagePlants are the image half of the coupling (F16c, G11): a Dockerfile that
// compiles the tagged binary publishes ee/ code, so its licence label has to say
// so -- and one that does not carry the tag keeps the MIT label the core's
// licence makes true (design §8).
var imagePlants = []struct {
	name    string
	bodies  map[string]string
	refused bool
	want    string
}{
	{
		name:    "a planted tagged base image with an MIT label is refused",
		bodies:  map[string]string{"base": dockerfilePlant(taggedBuild, mitLicenceLabel)},
		refused: true,
		want:    "images/base/Dockerfile",
	},
	{
		name:    "a planted tagged history image with an MIT label is refused",
		bodies:  map[string]string{"history": dockerfilePlant(taggedBuild, mitLicenceLabel)},
		refused: true,
		want:    "images/history/Dockerfile",
	},
	{
		name:    "a planted tagged base image with no licences label is refused",
		bodies:  map[string]string{"base": dockerfilePlant(taggedBuild, "")},
		refused: true,
		want:    "images/base/Dockerfile",
	},
	{
		name: "a planted tree whose tagged images name the Subscription Edition licence is accepted",
		bodies: map[string]string{
			"base":        dockerfilePlant(taggedBuild, subscriptionLicenceLabel),
			"history":     dockerfilePlant(taggedBuild, subscriptionLicenceLabel),
			"credentials": dockerfilePlant("", mitLicenceLabel),
		},
	},
	{
		name:   "a planted untagged credentials image with an MIT label is accepted",
		bodies: map[string]string{"credentials": dockerfilePlant("", mitLicenceLabel)},
	},
}

// taggedBuild is the build tag the two images that carry ee/ code pass to go
// build (D524, design §8).
const taggedBuild = "ee"

const (
	mitLicenceLabel          = `LABEL org.opencontainers.image.licenses="MIT"`
	subscriptionLicenceLabel = `LABEL org.opencontainers.image.licenses="MIT AND ` + subscriptionMarker + `"`
)

// dockerfilePlant is a Dockerfile of the shape the three published images
// share -- one build stage running `go build`, one licence label -- written so a
// plant can vary the tag and the label and nothing else.
func dockerfilePlant(tags, label string) string {
	flags := ""
	if tags != "" {
		flags = " -tags " + tags
	}
	body := "FROM golang:1.27-bookworm AS fugaro\n" +
		"RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath" + flags + " -o /out/fugaro ./cmd/fugaro\n"
	if label == "" {
		return body
	}
	return body + label + "\n"
}

// plantImages writes a tree carrying the Dockerfiles a plant names. The reader
// takes its list of images from the tree, so a plant writes only the image it is
// about.
func plantImages(t *testing.T, bodies map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range slices.Sorted(maps.Keys(bodies)) {
		writePlant(t, root, "images/"+name+"/Dockerfile", bodies[name])
	}
	return root
}

// imageTagLabelGaps reports every image in the tree whose Dockerfile compiles the
// tagged binary without a licence label naming the Subscription Edition licence.
// The tag is what puts ee/ code into the image, and the label is the one thing a
// person who pulls it reads before it runs (F16c, G11). The list comes from the
// tree rather than from a list written here, so an image added later is gated
// without anyone remembering this file -- the credentials image is the other half
// of the gate, carrying no ee/ code and keeping its MIT label (D524, design §8).
func imageTagLabelGaps(t *testing.T, root string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "images", "*", "Dockerfile"))
	if err != nil {
		t.Fatalf("globbing the image Dockerfiles under %s: %v", root, err)
	}
	if len(paths) == 0 {
		return []string{"the tree has no images/*/Dockerfile, so nothing gates the images that publish ee/ code"}
	}
	slices.Sort(paths)

	var gaps []string
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		rel = filepath.ToSlash(rel)
		body := readText(t, path)
		if !slices.Contains(dockerfileBuildTags(body), taggedBuild) {
			continue
		}
		if label := dockerfileLicencesLabel(body); !strings.Contains(label, subscriptionMarker) {
			gaps = append(gaps, fmt.Sprintf("%s builds the tagged binary and its org.opencontainers.image.licenses label is %q, which has to hold %s", rel, label, subscriptionMarker))
		}
	}
	return gaps
}

// dockerfileBuildTags is the build tag set a Dockerfile's own `go build` command
// passes. The RUN's continuation lines are joined first, so the flag reads the
// same wherever it sits in the command.
func dockerfileBuildTags(body string) []string {
	var tags []string
	for _, line := range strings.Split(strings.ReplaceAll(body, "\\\n", " "), "\n") {
		if strings.Contains(line, "go build") {
			tags = append(tags, goflagTags(line)...)
		}
	}
	return tags
}

// dockerfileLicencesLabel is the value of a Dockerfile's OCI licences label, in
// either the quoted or the bare form a LABEL may write.
func dockerfileLicencesLabel(body string) string {
	joined := strings.ReplaceAll(body, "\\\n", " ")
	match := dockerfileLicenceLabel.FindStringSubmatch(joined)
	if match == nil {
		return ""
	}
	if match[1] != "" {
		return match[1]
	}
	return match[2]
}

// dockerfileLicenceLabel reads the licences label out of a whole Dockerfile: the
// label is written once, and the value ends at the quote or the whitespace that
// closes it.
var dockerfileLicenceLabel = regexp.MustCompile(`org\.opencontainers\.image\.licenses=(?:"([^"]*)"|(\S+))`)

// eeTagLabelGaps is the coupling: when the tree's own release build sets the ee
// tag, every licence label has to name the Subscription Edition licence, and this reports
// each one that does not. The tag set and the labels come from the same tree, so
// the gate below cannot be dropped or unwired without a plant turning red.
func eeTagLabelGaps(t *testing.T, root string) []string {
	t.Helper()
	cfg := readGoreleaser(t, filepath.Join(root, ".goreleaser.yaml"))
	workflow := readText(t, filepath.Join(root, ".github", "workflows", "release.yml"))
	if !buildTagSet(t, cfg, []byte(workflow))["ee"] {
		return nil
	}
	return eeLabelGaps(t, cfg, root)
}

// labelNames are the six labels the Subscription Edition licence has to reach, one per
// place a reader learns what the binary is under.
var labelNames = []string{
	"the Homebrew cask",
	"the plugin manifest",
	"the README Licensing section",
	"LICENSING.md",
	"the community page's licence card",
	"the community page's #licence section",
}

// subscriptionMarker is the SPDX expression every label names once the release
// build compiles ee/ in (R3, D303 as amended).
const subscriptionMarker = "LicenseRef-Fugaro-Subscription"

// labels is one tree's worth of licence labels; the two plants differ only in
// whether they name the Subscription Edition licence.
type labels struct {
	cask      string
	plugin    string
	readme    string
	licensing string
	astroCard string
	astroSect string
}

var mitLabels = labels{
	cask:      "MIT",
	plugin:    "MIT",
	readme:    "## Licensing\n\nFugaro's core is released under the MIT licence in [LICENSE](LICENSE).\n",
	licensing: "# Licensing\n\nFugaro's core is under the MIT licence in `LICENSE`.\n",
	astroCard: `<a href="LICENSE">Licence</a> the {licence} licence`,
	astroSect: "<section id=\"licence\">\nFugaro is released under the {licence} licence.\n",
}

var subscriptionLabels = labels{
	cask:      subscriptionMarker,
	plugin:    subscriptionMarker,
	readme:    "## Licensing\n\nFugaro's core is under the MIT licence in [LICENSE](LICENSE); the released binary carries `ee/` code under " + subscriptionMarker + " ([ee/LICENSE](ee/LICENSE)).\n",
	licensing: "# Licensing\n\n`ee/` is under " + subscriptionMarker + " in `ee/LICENSE`.\n",
	astroCard: "<a href=\"LICENSE\">Licence</a> the {licence} licence; the released binary carries code under `ee/LICENSE`",
	astroSect: "<section id=\"licence\">\nThe binary carries code under " + subscriptionMarker + " (`ee/LICENSE`).\n",
}

// plantRelease writes a tree carrying the given labels beside the release build
// that decides the tag set -- the config and the workflow the coupling test reads
// them from -- so it can be run against a release that does set the ee tag
// without touching the real repository.
func plantRelease(t *testing.T, l labels, config, workflow string) string {
	t.Helper()
	root := t.TempDir()
	writePlant(t, root, ".goreleaser.yaml", config)
	writePlant(t, root, ".github/workflows/release.yml", workflow)
	writePlant(t, root, "README.md", l.readme)
	writePlant(t, root, "LICENSING.md", l.licensing)
	writePlant(t, root, "plugin/.claude-plugin/plugin.json",
		fmt.Sprintf("{\n  \"name\": \"fugaro\",\n  \"license\": %q\n}\n", l.plugin))
	writePlant(t, root, "website/src/pages/community.astro", l.astroCard+"\n"+l.astroSect+"</section>\n")
	return root
}

func writePlant(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// eeLabelGaps reports every licence label that does not name the enterprise
// licence, one string per label, naming the label so a failure says which one
// to change. The cask's license comes from the parsed config (it is
// .goreleaser.yaml's own field); the rest are read from the tree under root.
func eeLabelGaps(t *testing.T, cfg goreleaserConfig, root string) []string {
	t.Helper()
	var gaps []string

	cask := ""
	if len(cfg.HomebrewCasks) > 0 {
		cask = cfg.HomebrewCasks[0].License
	}
	if !strings.Contains(cask, subscriptionMarker) {
		gaps = append(gaps, fmt.Sprintf("the Homebrew cask's license is %q and has to hold %s", cask, subscriptionMarker))
	}

	manifest, err := os.ReadFile(filepath.Join(root, "plugin", ".claude-plugin", "plugin.json"))
	if err != nil {
		gaps = append(gaps, fmt.Sprintf("the plugin manifest cannot be read: %v", err))
	} else {
		var doc struct {
			License string `json:"license"`
		}
		if err := json.Unmarshal(manifest, &doc); err != nil {
			gaps = append(gaps, fmt.Sprintf("the plugin manifest does not parse: %v", err))
		} else if !strings.Contains(doc.License, subscriptionMarker) {
			gaps = append(gaps, fmt.Sprintf("the plugin manifest's license is %q and has to hold %s", doc.License, subscriptionMarker))
		}
	}

	if !namesSubscription(readmeLicensingSection(t, root)) {
		gaps = append(gaps, fmt.Sprintf("the README Licensing section has to hold %s or link ee/LICENSE", subscriptionMarker))
	}

	if !namesSubscription(readTextIfAny(t, filepath.Join(root, "LICENSING.md"))) {
		gaps = append(gaps, fmt.Sprintf("LICENSING.md has to hold %s or link ee/LICENSE", subscriptionMarker))
	}

	astro := readTextIfAny(t, filepath.Join(root, "website", "src", "pages", "community.astro"))
	card, section, ok := splitLicenceElements(astro)
	if !ok {
		gaps = append(gaps, "the community page has no #licence section")
	} else {
		if !namesSubscription(card) {
			gaps = append(gaps, fmt.Sprintf("the community page's licence card has to hold %s or link ee/LICENSE", subscriptionMarker))
		}
		if !namesSubscription(section) {
			gaps = append(gaps, fmt.Sprintf("the community page's #licence section has to hold %s or link ee/LICENSE", subscriptionMarker))
		}
	}
	return gaps
}

// namesSubscription reports whether a label says which licence the binary's ee/
// code is under: it either names the SPDX expression or links ee/LICENSE.
func namesSubscription(text string) bool {
	return strings.Contains(text, subscriptionMarker) || strings.Contains(text, "ee/LICENSE")
}

func readmeLicensingSection(t *testing.T, root string) string {
	return markdownSection(readTextIfAny(t, filepath.Join(root, "README.md")), "## Licensing")
}

// splitLicenceElements splits the community page at its #licence section: the
// page states the licence twice, once in the card above the section and once in
// the section itself, and both have to carry the same claim.
func splitLicenceElements(page string) (card, section string, ok bool) {
	idx := strings.Index(page, `id="licence"`)
	if idx < 0 {
		return "", "", false
	}
	return page[:idx], page[idx:], true
}

// markdownSection returns the body of the first section whose heading is
// exactly heading, up to the next level-2 heading.
func markdownSection(doc, heading string) string {
	idx := strings.Index(doc, heading)
	if idx < 0 {
		return ""
	}
	rest := doc[idx:]
	if next := strings.Index(rest[len(heading):], "\n## "); next >= 0 {
		return rest[:len(heading)+next]
	}
	return rest
}

// goreleaserConfig is the part of .goreleaser.yaml these tests read. Decoding
// into it ignores every field it does not name, so the file can grow.
type goreleaserConfig struct {
	Env           envList             `yaml:"env"`
	Before        goreleaserHooks     `yaml:"before"`
	Builds        []goreleaserBuild   `yaml:"builds"`
	Archives      []goreleaserArchive `yaml:"archives"`
	HomebrewCasks []goreleaserCask    `yaml:"homebrew_casks"`
}

type goreleaserHooks struct {
	Hooks []string `yaml:"hooks"`
}

// goreleaserBuild is one builds[] entry.
type goreleaserBuild struct {
	Main      string               `yaml:"main"`
	Goos      []string             `yaml:"goos"`
	Goarch    []string             `yaml:"goarch"`
	Tags      []string             `yaml:"tags"`
	Flags     []string             `yaml:"flags"`
	Env       envList              `yaml:"env"`
	Overrides []goreleaserOverride `yaml:"overrides"`
}

// goreleaserOverride is one overrides[] entry, which carries a single goos and
// the same tags, flags and env a build does.
type goreleaserOverride struct {
	Goos  string   `yaml:"goos"`
	Tags  []string `yaml:"tags"`
	Flags []string `yaml:"flags"`
	Env   envList  `yaml:"env"`
}

type goreleaserArchive struct {
	Files []archiveFile `yaml:"files"`
}

type goreleaserCask struct {
	License string `yaml:"license"`
}

// archiveFile is one archives[].files entry, which GoReleaser accepts either as
// a plain path or as a mapping with src/dst/glob.
type archiveFile struct {
	Src         string `yaml:"src"`
	Dst         string `yaml:"dst"`
	Glob        string `yaml:"glob"`
	StripParent bool   `yaml:"strip_parent"`
}

func (f *archiveFile) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		f.Src = node.Value
		return nil
	}
	type plain archiveFile
	var p plain
	if err := node.Decode(&p); err != nil {
		return err
	}
	*f = archiveFile(p)
	return nil
}

// envList is an env: block, which is either a list of KEY=value strings or a
// mapping; both spellings are normalised to KEY=value.
type envList []string

func (e *envList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var list []string
		if err := node.Decode(&list); err != nil {
			return err
		}
		*e = list
	case yaml.MappingNode:
		var m map[string]any
		if err := node.Decode(&m); err != nil {
			return err
		}
		var list []string
		for k, v := range m {
			list = append(list, fmt.Sprintf("%s=%v", k, v))
		}
		slices.Sort(list)
		*e = list
	}
	return nil
}

// releaseTags is the build tag set .goreleaser.yaml's builds compile. The
// notices file and the enumeration that checks it are both about the binary the
// archive ships, so both ask the toolchain for this build. A tree with no ee/ --
// the core-without-ee workflow deletes it -- has nothing for the tag to compile
// and cannot ask for it: the assembly file the tag builds imports a package that
// is not there.
func releaseTags(t *testing.T, root string) []string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "ee")); err != nil {
		return nil
	}
	cfg := readGoreleaser(t, filepath.Join(root, ".goreleaser.yaml"))
	return slices.Sorted(maps.Keys(buildTagSet(t, cfg, []byte(noTagWorkflow))))
}

// noticesScriptTags is the build tag set scripts/third-party-notices.sh hands
// go list -deps, read from the fake toolchain's own record of its arguments: the
// script reads the tags out of the config in shell, and this is what holds that
// reading to the config.
func noticesScriptTags(t *testing.T, root string) []string {
	t.Helper()
	bin := t.TempDir()
	args := filepath.Join(t.TempDir(), "args")
	modules := t.TempDir()
	writeModuleDir(t, modules, "everywhere", "LICENSE", "the everywhere module's licence text\n")
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}
	runNoticesScript(t, root, filepath.Join(t.TempDir(), noticesFile), []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_MODULE_DIR=" + modules,
		"FAKE_ARGS_FILE=" + args,
	})

	tags := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(readText(t, args)), "\n") {
		for _, tag := range goflagTags(line) {
			tags[tag] = true
		}
	}
	return slices.Sorted(maps.Keys(tags))
}

func readGoreleaser(t *testing.T, path string) goreleaserConfig {
	t.Helper()
	return parseGoreleaser(t, readText(t, path))
}

func parseGoreleaser(t *testing.T, body string) goreleaserConfig {
	t.Helper()
	var cfg goreleaserConfig
	if err := yaml.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("parsing the goreleaser config: %v", err)
	}
	return cfg
}

// buildTagSet is the union of every build tag the release build would set: each
// build's tags, the -tags in its flags, GOFLAGS in its env or in the top-level
// env, the same three in each of its overrides, and GOFLAGS in the env of every
// goreleaser step of the release workflow — the workflow's environment is what
// the build runs in, so a tag set there is as effective as one in the config.
func buildTagSet(t *testing.T, cfg goreleaserConfig, workflow []byte) map[string]bool {
	t.Helper()
	tags := map[string]bool{}
	add := func(values ...string) {
		for _, value := range values {
			for _, tag := range strings.Split(value, ",") {
				if tag = strings.TrimSpace(tag); tag != "" {
					tags[tag] = true
				}
			}
		}
	}
	addEnv := func(env envList) {
		for _, entry := range env {
			if name, value, ok := strings.Cut(entry, "="); ok && name == "GOFLAGS" {
				add(goflagTags(value)...)
			}
		}
	}
	for _, b := range cfg.Builds {
		add(b.Tags...)
		add(goflagTags(strings.Join(b.Flags, " "))...)
		addEnv(b.Env)
		for _, o := range b.Overrides {
			add(o.Tags...)
			add(goflagTags(strings.Join(o.Flags, " "))...)
			addEnv(o.Env)
		}
	}
	addEnv(cfg.Env)
	add(workflowGOFLAGTags(t, workflow)...)
	return tags
}

// goflagTags reads the value of every -tags flag in a flag string, in both the
// -tags=a,b form and the -tags a,b form.
func goflagTags(flags string) []string {
	var tags []string
	fields := strings.Fields(flags)
	for i := 0; i < len(fields); i++ {
		if value, ok := strings.CutPrefix(fields[i], "-tags="); ok {
			tags = append(tags, value)
			continue
		}
		if fields[i] == "-tags" && i+1 < len(fields) {
			tags = append(tags, fields[i+1])
			i++
		}
	}
	return tags
}

// workflowGOFLAGTags reads GOFLAGS from every environment a goreleaser step
// runs in: the workflow's own env, the env of a job holding such a step, and
// the step's own env. A step's `run:` or another action's env is not the
// build's, so only a goreleaser step's counts.
func workflowGOFLAGTags(t *testing.T, workflow []byte) []string {
	t.Helper()
	var doc struct {
		Env  envList `yaml:"env"`
		Jobs map[string]struct {
			Env   envList `yaml:"env"`
			Steps []struct {
				Uses string  `yaml:"uses"`
				Env  envList `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(workflow, &doc); err != nil {
		t.Fatalf("parsing the release workflow: %v", err)
	}
	var tags []string
	add := func(env envList) {
		for _, entry := range env {
			if name, value, ok := strings.Cut(entry, "="); ok && name == "GOFLAGS" {
				tags = append(tags, goflagTags(value)...)
			}
		}
	}
	add(doc.Env)
	for _, job := range doc.Jobs {
		var goreleaser []envList
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "goreleaser/goreleaser-action@") {
				goreleaser = append(goreleaser, step.Env)
			}
		}
		if len(goreleaser) == 0 {
			continue
		}
		add(job.Env)
		for _, env := range goreleaser {
			add(env)
		}
	}
	return tags
}

// reachesEE matches the reserved ee/ directory by path segment, the way the
// boundary tests do: .../eevee is not a match.
func reachesEE(path string) bool {
	path = filepath.ToSlash(path)
	return path == "ee" || strings.HasPrefix(path, "ee/") ||
		strings.HasSuffix(path, "/ee") || strings.Contains(path, "/ee/")
}

// noticesHookOutput returns the path the notices hook writes to. It is an
// argument in .goreleaser.yaml rather than a default inside the script, so the
// path the archive carries is visible in the config the archive is built from.
func noticesHookOutput(t *testing.T, cfg goreleaserConfig) string {
	t.Helper()
	for _, hook := range cfg.Before.Hooks {
		fields := strings.Fields(hook)
		for i, field := range fields {
			if !strings.HasSuffix(field, noticesScript) {
				continue
			}
			if i+1 >= len(fields) {
				t.Fatalf("the before hook %q runs %s with no output path: the path the archive carries has to be in the config", hook, noticesScript)
			}
			return fields[i+1]
		}
	}
	t.Fatalf("no before hook runs %s, so the release generates no third-party notices: before.hooks is %v", noticesScript, cfg.Before.Hooks)
	return ""
}

// noticesCommand is the script invoked the way the before hook invokes it, with
// extraEnv added to this process's environment (the fake-go tests replace PATH
// with it).
func noticesCommand(root, out string, extraEnv []string) *exec.Cmd {
	cmd := exec.Command("bash", filepath.Join(root, "scripts", noticesScript), out)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), extraEnv...)
	return cmd
}

// runNoticesScript runs the script into out, failing the test when it does not
// exit 0: a release whose hook fails ships nothing.
func runNoticesScript(t *testing.T, root, out string, extraEnv []string) {
	t.Helper()
	if got, err := noticesCommand(root, out, extraEnv).CombinedOutput(); err != nil {
		t.Fatalf("scripts/%s %s: %v\n%s", noticesScript, out, err, got)
	}
}

// linkedModules runs go list -deps once per target of the build matrix, the way
// the script does, and returns "path version" -> module directory for every
// module other than this one.
func linkedModules(t *testing.T, root string) map[string]string {
	t.Helper()
	modules := map[string]string{}
	args := []string{"list", "-deps", "-f", moduleLine}
	if tags := releaseTags(t, root); len(tags) > 0 {
		args = append(args, "-tags="+strings.Join(tags, ","))
	}
	args = append(args, "./cmd/fugaro")
	for _, target := range noticesTargets {
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS="+target.goos, "GOARCH="+target.goarch, "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list -deps for %s/%s: %v", target.goos, target.goarch, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) != 3 {
				continue
			}
			modules[fields[0]+" "+fields[1]] = fields[2]
		}
	}
	return modules
}

// licenceFiles lists the licence and notice files in a module directory, the
// way the script finds them, so a module that ships one under a name the script
// does not look for is a failure and not a silent omission.
func licenceFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		lower := strings.ToLower(e.Name())
		for _, prefix := range []string{"license", "copying", "notice"} {
			if strings.HasPrefix(lower, prefix) {
				names = append(names, e.Name())
				break
			}
		}
	}
	slices.Sort(names)
	return names
}

// noticesSection returns the section a module's heading opens, or "" when the
// file has none.
func noticesSection(notices, key string) string {
	heading := "## " + key
	idx := strings.Index(notices, "\n"+heading)
	if idx < 0 {
		return ""
	}
	rest := notices[idx+1:]
	if next := strings.Index(rest[len(heading):], "\n## "); next >= 0 {
		return rest[:len(heading)+next]
	}
	return rest
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// readTextIfAny reads a file the release may not have yet: at 0.11.0 the label
// check only runs for a release that sets the ee tag, and the files it reads
// land with the other 0.11.0 tasks.
func readTextIfAny(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
