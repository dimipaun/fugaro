package scripts_test

// The build definition and the deploy that reads it, held together by reading
// both as text: ee/keyservice/cloudbuild.yaml is P1.7's and is submitted by
// scripts/deploy-portal.sh, which is P9.2's, and neither file can see the other
// except through what they name. The deploy's own comment says why that
// matters (deploy-portal.sh's, above its `gcloud builds submit`): the script
// deploys `$_REPOSITORY/fugaro-portal-api:$_TAG` and
// `$_REPOSITORY/fugaro-key-minter:$_TAG`, so a config that named its own images
// or guessed its own tag — `$SHORT_SHA`, which is empty on a manual submit —
// would push images the deploy never names, or a tag that already exists, which
// is the previous image serving under the new revision's name.
//
// Nothing here reaches a cloud: both files are read, and every claim is about
// the words in them.

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// cloudBuild is the part of a Cloud Build config this case reads: the fields the
// deploy's contract runs through, and no others. It is decoded loosely rather
// than strictly, because the rest of a build config is the build's own business
// — a timeout, a machine type, a logging option — and a case that refused one
// would fail on a change that has nothing to do with what the deploy names. What
// is strict is the contract below: every step's own tags are read, so a third
// build is a tag this case sees.
type cloudBuild struct {
	Substitutions map[string]string `yaml:"substitutions"`
	Images        []string          `yaml:"images"`
	Steps         []struct {
		ID   string   `yaml:"id"`
		Args []string `yaml:"args"`
	} `yaml:"steps"`
}

// deployService matches one `gcloud run deploy <service>` of the script, and
// deployImage the `--image "<base>/<name>:<tag>"` beside it.
var (
	deployService   = regexp.MustCompile(`gcloud run deploy\s+(\S+)`)
	deployImage     = regexp.MustCompile(`--image\s+"([^"]+)"`)
	submissionFlags = regexp.MustCompile(`--substitutions="([^"]*)"`)
)

// TestCloudBuildPushesWhatTheDeployNames is the whole of the contract in three
// claims: the two images the config pushes are the two the deploy deploys, the
// tag is the deploy's own, and the substitutions the config declares are the
// ones the deploy passes — no more (a name the submitter must invent) and no
// fewer (a name the config reads and nobody sets, which Cloud Build answers with
// an empty string).
func TestCloudBuildPushesWhatTheDeployNames(t *testing.T) {
	root := testutil.ModuleRoot()
	script := readRepoFile(t, filepath.Join(root, "scripts", "deploy-portal.sh"))

	var build cloudBuild
	raw := readRepoFile(t, filepath.Join(root, "ee", "keyservice", "cloudbuild.yaml"))
	if err := yaml.NewDecoder(strings.NewReader(raw)).Decode(&build); err != nil {
		t.Fatalf("ee/keyservice/cloudbuild.yaml is not a Cloud Build config this case can read: %v", err)
	}

	// The substitutions the script passes, which are the names after each `=`,
	// and the two values it passes them from: the image base and the commit the
	// tree's own HEAD is (the script refuses a dirty tree over exactly this).
	passed := map[string]bool{}
	for _, value := range submissionFlags.FindAllStringSubmatch(script, -1) {
		for _, pair := range strings.Split(value[1], ",") {
			name, _, ok := strings.Cut(pair, "=")
			if !ok {
				t.Fatalf("the deploy passes the substitution %q, which is not a NAME=value pair", pair)
			}
			passed[name] = true
		}
	}
	if len(passed) == 0 {
		t.Fatal("scripts/deploy-portal.sh passes no --substitutions= to `gcloud builds submit`, so the config below is left with its empty defaults and its first docker build fails")
	}
	for name := range build.Substitutions {
		if !passed[name] {
			t.Errorf("ee/keyservice/cloudbuild.yaml declares %s and the deploy passes %s: a substitution the submitter does not set is empty at the build, and the deploy's own words are what the images have to be named after", name, names(passed))
		}
	}
	for name := range passed {
		if _, ok := build.Substitutions[name]; !ok {
			t.Errorf("the deploy passes %s and ee/keyservice/cloudbuild.yaml does not declare it: Cloud Build refuses a substitution the config never declares, so the whole build fails before either image is built", name)
		}
	}

	if len(build.Images) != 2 {
		t.Fatalf("ee/keyservice/cloudbuild.yaml pushes %d image(s), want 2: the deploy deploys the API and the minter, and a build that pushed a third would be pushing an image nothing here names", len(build.Images))
	}
	// Every image is the deploy's own spelling: the substitutions put back to the
	// script's variables, which is what the script's `--image` flags are written
	// in. The two service names come out of that same substitution, so a rename
	// on either side is one of these two lines failing.
	deployed := map[string]string{}
	for _, match := range deployService.FindAllStringSubmatch(script, -1) {
		deployed[match[1]] = ""
	}
	images := map[string]bool{}
	for _, image := range build.Images {
		inScript := strings.NewReplacer(
			"${_REPOSITORY}", "$image_base",
			"${_TAG}", "$tag",
		).Replace(image)
		if !strings.Contains(script, `"`+inScript+`"`) {
			t.Errorf("ee/keyservice/cloudbuild.yaml pushes %s, and scripts/deploy-portal.sh has no --image %q: the revision the deploy makes is the image this build pushed, and an image pushed under another name is one the deploy cannot name (it would deploy whatever that tag already held, which is a release behind)", image, inScript)
		}
		if _, ok := deployed[serviceOf(inScript)]; !ok {
			t.Errorf("ee/keyservice/cloudbuild.yaml pushes %s, and scripts/deploy-portal.sh deploys %s: the two services this build pushes for are the two the deploy revises (fugaro-portal-api and fugaro-key-minter)", image, names(deployed))
		}
		images[image] = true
	}
	if len(deployed) != len(images) {
		t.Errorf("the deploy names %d service(s) (%s) and the build pushes %d image(s) (%s): the deploy and the build have to be about the same two images, or one of them is stale", len(deployed), names(deployed), len(images), names(images))
	}

	// And the tag each step builds with is the tag the config pushes: a step that
	// tagged `latest` beside an `images:` entry naming the commit would push an
	// image the deploy does not deploy, and the build would look green.
	tagged := 0
	for _, step := range build.Steps {
		tags := tagArgs(step.Args)
		if len(tags) == 0 {
			t.Errorf("the step %q builds no tagged image, so the images: list above has nothing to push: every step here is one docker build", step.ID)
			continue
		}
		for _, tag := range tags {
			tagged++
			if !images[tag] {
				t.Errorf("the step %q tags %s, which the images: list does not push (it pushes %s): the deploy deploys the pushed tags, so an image built under another name is one more image the registry keeps and nothing deploys", step.ID, tag, names(images))
			}
		}
	}
	if tagged != len(build.Images) {
		t.Errorf("the steps tag %d image(s) and the images: list pushes %d: the two are one list of two images, and a count that differs means a tag either side wrote for the other", tagged, len(build.Images))
	}

	// A tag the submitter cannot set is worse than a missing one, and `$SHORT_SHA`
	// is exactly that: empty on a manual submit, and a build that fell back to
	// `latest` is the previous image under the new revision's name. What is read
	// is what the config uses — the pushed images and the steps' own argv — and
	// not the file's text, because the file's own comment names the two
	// substitutions it refuses and a search of the prose would fail on the
	// sentence that says why.
	for _, written := range append(slices.Clone(build.Images), stepArgs(build.Steps)...) {
		for _, forbidden := range []string{"SHORT_SHA", "_REGION", ":latest"} {
			if strings.Contains(written, forbidden) {
				t.Errorf("ee/keyservice/cloudbuild.yaml uses %q, which writes %s: the tag is the deploy's own (--substitutions=_REPOSITORY=...,_TAG=...), and a tag this file invents is one the deploy's own --image flags do not name", written, forbidden)
			}
		}
	}
}

// TestCloudBuildStepsBuildingPlatformDockerfilesUseBuildKit holds the fault that
// failed the first submit at step 0: a Dockerfile with BUILDPLATFORM (or
// TARGETARCH, set from it) only builds under BuildKit, and the docker builder
// image runs the legacy builder unless DOCKER_BUILDKIT=1 is in the step's env.
func TestCloudBuildStepsBuildingPlatformDockerfilesUseBuildKit(t *testing.T) {
	root := testutil.ModuleRoot()
	var build struct {
		Steps []struct {
			ID   string   `yaml:"id"`
			Env  []string `yaml:"env"`
			Args []string `yaml:"args"`
		} `yaml:"steps"`
	}
	raw := readRepoFile(t, filepath.Join(root, "ee", "keyservice", "cloudbuild.yaml"))
	if err := yaml.NewDecoder(strings.NewReader(raw)).Decode(&build); err != nil {
		t.Fatalf("ee/keyservice/cloudbuild.yaml is not a Cloud Build config this case can read: %v", err)
	}
	checked := 0
	for _, step := range build.Steps {
		for i, arg := range step.Args {
			if arg != "--file" || i+1 >= len(step.Args) {
				continue
			}
			dockerfile := readRepoFile(t, filepath.Join(root, step.Args[i+1]))
			if !strings.Contains(dockerfile, "BUILDPLATFORM") {
				continue
			}
			checked++
			if !slices.Contains(step.Env, "DOCKER_BUILDKIT=1") {
				t.Errorf("the step %q builds %s, which uses BUILDPLATFORM, without DOCKER_BUILDKIT=1 in its env: the legacy builder fails it with `failed to parse platform`", step.ID, step.Args[i+1])
			}
		}
	}
	if checked == 0 {
		t.Error("no step builds a Dockerfile with BUILDPLATFORM, so this case checks nothing: the Dockerfiles or the steps moved")
	}
}

// stepArgs answers every argument the build's steps carry, in order, which is
// what the last check reads: the images a step builds are named in its argv.
func stepArgs(steps []struct {
	ID   string   `yaml:"id"`
	Args []string `yaml:"args"`
}) []string {
	var out []string
	for _, step := range steps {
		out = append(out, step.Args...)
	}
	return out
}

// tagArgs answers the `--tag <image>` values of one step's argv, in order.
func tagArgs(args []string) []string {
	var out []string
	for i, arg := range args {
		if arg == "--tag" && i+1 < len(args) {
			out = append(out, args[i+1])
		}
	}
	return out
}

// serviceOf answers the service an image belongs to: `<base>/<name>:<tag>`'s
// name, which is the Cloud Run service the deploy revises.
func serviceOf(image string) string {
	named, _, _ := strings.Cut(image, ":")
	return named[strings.LastIndex(named, "/")+1:]
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// names answers a string-keyed set's keys, sorted, for a failure message: the
// order a map hands them over in is not one a reader can compare two runs by.
func names[V any](set map[string]V) string {
	return strings.Join(slices.Sorted(maps.Keys(set)), ", ")
}
