package images_test

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The second half of issue #1745 is the nightly's republish: the nightly builds
// the latest release tag's tree and pushes it under that release's tags, so
// before this change every quiet night moved :VERSION, :VERSION-arch and
// :MAJOR onto a build whose only difference was the clock. publish-base now
// decides that question first, in a step of its own, and these tests are its
// two halves: the workflow text says when the step runs and what it gates
// (publishSkipProblems, and the mutations that prove those checks are live),
// and the shell the step runs is executed here against a docker CLI stub, so
// the decision itself is a test rather than a promise in a comment. A third
// half is the reader's: the issue asked what the check's "the base moved" rule
// reads once the tags stop moving, and whether an installation still pins
// `-pinned`, and the answers live in docs/base-image.md, where
// TestBaseImageDocAnswersTheBaseMovedQuestion keeps them.

// TestBaseImageDocAnswersTheBaseMovedQuestion: the change is invisible from
// outside the workflow unless the base-image doc says it, and a sentence there
// that goes stale is worse than none — a reader who still believes every
// nightly moves `:VERSION` pins for the wrong reason, and one who believes a
// quiet night cannot move it stops verifying. The section has to name what the
// comparison reads, that the tags stay put, and that the pin is still what a
// job's base rests on; the staging is named too, because until the owner copies
// the workflow in the described behaviour is not the behaviour that runs.
func TestBaseImageDocAnswersTheBaseMovedQuestion(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "docs", "base-image.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	const heading = "## The published tags, and what the nightly does"
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("docs/base-image.md has no %q section: the base-moved question (issue #1745) is answered there or nowhere the reader looks", heading)
	}
	section := doc[start:]
	if end := strings.Index(section[len(heading):], "\n## "); end >= 0 {
		section = section[:len(heading)+end]
	}
	for _, want := range []string{
		// What the decision reads, so the doc and publishSkipProblems name the
		// same comparison rather than two descriptions of "the same base".
		"config digest",
		"the tree's own `fugaro` binary",
		"the installed package list",
		// The two answers: the tags stay where they are, and the pin is the
		// signature over a digest rather than a tag that cannot move.
		"leaves the tags where they are",
		"still pin `-pinned`",
		"signature",
		// What the check's rule reads, and that the change is staged.
		"the digest that reference resolves to",
		"docs/pending-workflows/images.yml",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("docs/base-image.md's %q section lacks %q: the base-moved question has to be answered where a reader looks, not only in the workflow", heading, want)
		}
	}
}

// TestPublishBaseSkipsAnUnchangedNightly: the decision step is exactly the
// thing that must not run on a tag push, before the push it decides, and wired
// to the step it gates.
func TestPublishBaseSkipsAnUnchangedNightly(t *testing.T) {
	for _, p := range publishSkipProblems(parseImagesWorkflow(t, readImagesWorkflow(t))) {
		t.Error(p)
	}
}

// publishSkipProblems names every way the "is this the published build?"
// decision can be weakened: a gate that lets it run on a release, a decision
// that is never handed to the step it gates (so the nightly publishes whatever
// it found), a decision made after the push it was meant to prevent, a
// fingerprint that cannot tell another tree's base from this one, or a step
// that pushes.
func publishSkipProblems(wf imagesWorkflow) []string {
	var out []string
	bad := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }

	job, ok := wf.Jobs["publish-base"]
	if !ok {
		return append(out, "no publish-base job")
	}
	at, steps := map[string]int{}, map[string]workflowStep{}
	for i, s := range job.Steps {
		if _, dup := at[s.ID]; dup && s.ID != "" {
			bad("two publish-base steps share the id %q, so a later one silently wins", s.ID)
		}
		at[s.ID], steps[s.ID] = i, s
	}
	dec, ok := steps["unchanged"]
	if !ok {
		return append(out, "publish-base has no \"unchanged\" step: the nightly has nothing deciding whether the published tags already name this build, so every quiet night republishes")
	}
	idx, ok := steps["index"]
	if !ok {
		return append(out, "publish-base has no index step (id \"index\")")
	}

	// The step is the nightly's: a tag push publishes and signs whatever the
	// registry already says, so a re-run of a release can never become a run
	// whose index step was skipped and whose sign step has no digest.
	if got := strings.TrimSpace(dec.If); got != "github.event_name != 'push'" {
		bad("the unchanged step's if is %q, want exactly %q: a tag push always pushes (and signs, and writes the -pinned tag), and only a nightly or a dispatch may be turned into a no-op", got, "github.event_name != 'push'")
	}
	if at["unchanged"] > at["index"] {
		bad("the unchanged step runs after the index step it decides (at %d and %d): by then the push has happened and the tags have moved", at["unchanged"], at["index"])
	}
	if strings.Contains(script(dec.Run), "docker push") {
		bad("the unchanged step pushes something: the decision has to be made before any push, because a push is what moves the tag")
	}
	if !strings.Contains(script(dec.Run), `echo "unchanged=$unchanged" >> "$GITHUB_OUTPUT"`) {
		bad(`the unchanged step does not hand its decision on ("echo \"unchanged=$unchanged\" >> \"$GITHUB_OUTPUT\"") : the index step below is gated on an output nobody writes, which reads as "changed" and republishes every night`)
	}
	// The gate itself: the index step has to be skippable by the decision AND
	// unable to be skipped on a tag push.
	gate := strings.TrimSpace(idx.If)
	if !strings.Contains(gate, "steps.unchanged.outputs.unchanged != 'true'") {
		bad("the index step's if is %q: it does not read the unchanged step's decision, so the decision changes nothing", gate)
	}
	if !strings.Contains(gate, "github.event_name == 'push'") {
		bad("the index step's if is %q: on a tag push it must run whatever the decision says, because the sign steps below need the digest it reports", gate)
	}
	// What the fingerprint reads: the pins the build says it used, the tree's
	// own binary -- Version and Commit are linked into it, so a different tree
	// cannot answer "same" -- and what dpkg installed. A fingerprint missing
	// any of the three compares less than "is this the same base?".
	for _, want := range []string{"/etc/fugaro/base.json", "/usr/local/bin/fugaro", "/var/lib/dpkg/status"} {
		if !strings.Contains(script(dec.Run), want) {
			bad("the unchanged step's fingerprint does not read %s: the comparison has to be of what the image is, not of one file of it", want)
		}
	}
	if !strings.Contains(script(dec.Run), "docker create") {
		bad("the unchanged step does not read the published image with docker create and docker cp: running it would need the other architecture to be emulated on this runner")
	}
	// A read that fails has no answer, and the step may only compare answers.
	// Bash turns errexit off inside a command substitution, so an unchecked
	// docker read falls through to the sha256sum below -- which hashes the
	// empty files and prints a digest -- and two such answers compare equal:
	// "I could not read the image" would become unchanged=true and the nightly
	// would skip the publish it exists to make. Every read therefore returns
	// non-zero, and only fingerprints that succeeded are compared.
	for _, read := range []string{
		`cid=$(docker create "$1")`,
		`docker cp "$cid:/etc/fugaro/base.json" "$dir/base.json"`,
		`docker cp "$cid:/usr/local/bin/fugaro" "$dir/fugaro"`,
		`docker cp "$cid:/var/lib/dpkg/status" "$dir/dpkg-status"`,
	} {
		if !strings.Contains(script(dec.Run), read+" || return 1") {
			bad("the unchanged step's fingerprint does not check %s (`|| return 1`): a read that failed gives no answer, and an unchecked one gives the hash of the empty files, which equals the other side's empty answer -- so an image nothing could be read from would be called unchanged and the publish skipped", read)
		}
	}
	if !strings.Contains(script(dec.Run), `a=$(fingerprint "$repo:$VERSION-$arch") && b=$(fingerprint fugaro-base:ci)`) ||
		!strings.Contains(script(dec.Run), `[ -n "$a" ] && [ "$a" = "$b" ]`) {
		bad(`the unchanged step does not compare only fingerprints that answered (want the two substitutions in one "&&" chain and "[ -n \"$a\" ] && [ \"$a\" = \"$b\" ]"): an empty answer equals another empty answer, and the step would skip a publish it could not decide`)
	}
	return out
}

// TestPublishSkipProblemsFindEveryMutation proves the checks above, as
// TestSigningProblemsFindEveryMutation does for the signing chain: each
// mutation is one weakening a reviewer would look for, and it must be named by
// the check that is about it.
func TestPublishSkipProblemsFindEveryMutation(t *testing.T) {
	base := parseImagesWorkflow(t, readImagesWorkflow(t))
	if got := publishSkipProblems(base); len(got) != 0 {
		t.Fatalf("the real workflow already reports problems: %q", got)
	}
	mutations := []struct {
		what string
		do   func(imagesWorkflow) imagesWorkflow
		want string
	}{
		{"the decision step is dropped", func(wf imagesWorkflow) imagesWorkflow {
			return withoutStep(wf, "unchanged")
		}, "has no \"unchanged\" step"},
		{"the decision is made after the push it prevents", func(wf imagesWorkflow) imagesWorkflow {
			return moveStep(wf, "unchanged", len(wf.Jobs["publish-base"].Steps)-1)
		}, "runs after the index step"},
		{"the decision step is widened to run on a tag push too", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("unchanged", func(s *workflowStep) { s.If = "" })
		}, "the unchanged step's if is"},
		{"the decision is never handed to the step it gates", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("unchanged", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `echo "unchanged=$unchanged" >> "$GITHUB_OUTPUT"`, `echo "unchanged=$unchanged"`, 1)
			})
		}, "does not hand its decision on"},
		{"the index step stops reading the decision", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("index", func(s *workflowStep) { s.If = "" })
		}, "does not read the unchanged step's decision"},
		{"the index step is skipped on a tag push as well", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("index", func(s *workflowStep) {
				s.If = "steps.unchanged.outputs.unchanged != 'true'"
			})
		}, "on a tag push it must run"},
		{"the fingerprint stops reading the tree's binary", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("unchanged", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `docker cp "$cid:/usr/local/bin/fugaro" "$dir/fugaro"`, "", 1)
			})
		}, "does not read /usr/local/bin/fugaro"},
		{"the fingerprint's read of the image is unchecked", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("unchanged", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `cid=$(docker create "$1") || return 1`, `cid=$(docker create "$1")`, 1)
			})
		}, "does not check cid=$(docker create"},
		{"the comparison accepts a fingerprint that failed", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("unchanged", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `[ -n "$a" ] && [ "$a" = "$b" ]`, `[ "$a" = "$b" ]`, 1)
			})
		}, "does not compare only fingerprints that answered"},
		{"the decision step pushes what it is deciding about", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("unchanged", func(s *workflowStep) {
				s.Run += "\ndocker push \"$repo:$VERSION-amd64\"\n"
			})
		}, "pushes something"},
	}
	for _, m := range mutations {
		got := publishSkipProblems(m.do(cloneWorkflow(base)))
		if len(got) == 0 {
			t.Errorf("%s: publishSkipProblems reported nothing", m.what)
			continue
		}
		var found bool
		for _, p := range got {
			if strings.Contains(p, m.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no problem names it (want a message containing %q), got %q", m.what, m.want, got)
		}
	}
}

// fakeDocker is the docker CLI the decision step is run against: a registry,
// two images and a container filesystem, in a directory of files. Only the
// calls the step makes are answered, and anything else fails the stub -- a
// step that grows a call this test does not model must be red here rather than
// quietly answer nothing. The state files it reads are written by the test:
// local_id_<arch> (the freshly built image's config digest), published_id_<arch>
// (the published manifest's, absent for a tag that is not there),
// pullable_<arch>, refs/<key>/{base.json,fugaro,dpkg-status}, keyed by the
// image reference with its separators replaced, and the two files that make a
// read of an image fail: create_fails (docker create refuses the image, which
// is what an arm64 image the runner cannot create looks like) and cp_fails
// (docker cp finds no such file in it). Both fail for every reference on
// purpose: the danger is a step that reads nothing from either side and calls
// the two empty answers equal.
const fakeDocker = `#!/bin/sh
# A docker CLI stub (images/base_publish_skip_test.go).
set -eu
state=${FAKE_STATE:?}
printf '%s\n' "$*" >> "$state/calls"
case "${1:-}" in
  load)
    cat > /dev/null
    n=$(cat "$state/loads" 2>/dev/null || echo 0)
    n=$((n + 1))
    printf '%s\n' "$n" > "$state/loads"
    # build-base.sh builds amd64 first and arm64 second.
    if [ "$n" = 1 ]; then printf 'amd64\n' > "$state/arch"; else printf 'arm64\n' > "$state/arch"; fi
    ;;
  image)
    cat "$state/local_id_$(cat "$state/arch")"
    ;;
  buildx)
    ref=${5:-}
    arch=${ref##*-}
    if [ -f "$state/published_id_$arch" ]; then
      printf '{"schemaVersion":2,"config":{"digest":"%s"},"layers":[]}\n' "$(cat "$state/published_id_$arch")"
    else
      echo "ERROR: $ref: not found" >&2
      exit 1
    fi
    ;;
  pull)
    arch=${3##*-}
    if [ -f "$state/pullable_$arch" ]; then exit 0; fi
    echo "Error: $3: manifest unknown" >&2
    exit 1
    ;;
  create)
    if [ -f "$state/create_fails" ]; then
      echo "Error response from daemon: No such image: ${2:-}" >&2
      exit 1
    fi
    printf '%s\n' "${2:-}" > "$state/cid"
    printf 'fakecid\n'
    ;;
  cp)
    key=$(printf '%s' "$(cat "$state/cid")" | tr '/:@' '___')
    if [ -f "$state/cp_fails" ]; then
      echo "Error response from daemon: Could not find the file $(basename "${3:-}") in the container" >&2
      exit 1
    fi
    cp "$state/refs/$key/$(basename "${3:-}")" "${3:-}"
    ;;
  rm)
    ;;
  *)
    echo "fake docker: unexpected call: $*" >&2
    exit 1
    ;;
esac
`

// publishSkipCase is one run of the decision: what the registry answers, what
// the two images contain, and what the nightly must conclude.
type publishSkipCase struct {
	name string
	// config[arch][side] is the config digest side reports: "local" is the
	// freshly built image (docker image inspect), "published" is what the
	// published tag's manifest says. An empty "published" is a tag that is not
	// there.
	config map[string]map[string]string
	// content[arch][side] is what the three fingerprinted files hold. Sides
	// with equal strings have equal fingerprints.
	content map[string]map[string]string
	// pullable[arch] is whether the published tag can be pulled.
	pullable map[string]bool
	// want is the value the step must write to $GITHUB_OUTPUT.
	want string
	// wantPull is whether the step must have tried to pull the published
	// image: it must not when the digests already agree (the cheap answer),
	// and must when they do not.
	wantPull bool
	// readFails is how reading the images fails, if it does: "create"
	// (docker create refuses them) or "cp" (docker cp finds no such file in
	// them), on both sides. Either way the step has no answer, and "no answer"
	// has to end in a publish rather than in a skip.
	readFails string
}

// TestTheUnchangedStepDecidesOnStubs runs the decision step's own shell, with
// the stub above for docker, over the cases the nightly can be in. The step is
// where the nightly's behaviour lives, so it is run rather than read: an
// always-true decision, an always-false one, a fast path that pulls anyway, or
// a fingerprint that cannot tell two bases apart would each be a nightly that
// republishes (or, worse, one that serves a stale image under a moved tag) and
// each of those is a case below.
func TestTheUnchangedStepDecidesOnStubs(t *testing.T) {
	for _, tool := range []string{"bash", "jq", "gunzip"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s on PATH: the decision step is run, not read, so this test needs one", tool)
		}
	}
	wf := parseImagesWorkflow(t, readImagesWorkflow(t))
	dec, ok := stepByID(wf, "publish-base", "unchanged")
	if !ok {
		t.Fatal("no unchanged step to run")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	step := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(step, []byte(dec.Run), 0o644); err != nil {
		t.Fatal(err)
	}
	// The step loads the two archives the build legs uploaded; gunzip is real,
	// so what it reads has to be one.
	for _, arch := range []string{"amd64", "arm64"} {
		writeGzip(t, filepath.Join(dir, "fugaro-base-"+arch+".tar.gz"))
	}

	const (
		repo    = "ghcr.io/dimipaun/fugaro-base"
		version = "0.19.0"
		local   = "fugaro-base:ci"
	)
	published := func(arch string) string { return repo + ":" + version + "-" + arch }
	// Both sides identical: the published image is this build.
	same := map[string]map[string]string{
		"amd64": {"local": "sha256:aa", "published": "sha256:aa"},
		"arm64": {"local": "sha256:bb", "published": "sha256:bb"},
	}
	sameContent := map[string]map[string]string{
		"amd64": {"local": "pins", "published": "pins"},
		"arm64": {"local": "pins", "published": "pins"},
	}
	cases := []publishSkipCase{
		{
			name:     "the published tag already names this build",
			config:   same,
			content:  sameContent,
			pullable: map[string]bool{"amd64": true, "arm64": true},
			want:     "true",
		},
		{
			// The clock moved and nothing else: different bytes, the same base.
			name: "another clock, the same pins and packages",
			config: map[string]map[string]string{
				"amd64": {"local": "sha256:aa", "published": "sha256:dd"},
				"arm64": {"local": "sha256:bb", "published": "sha256:ee"},
			},
			content:  sameContent,
			pullable: map[string]bool{"amd64": true, "arm64": true},
			want:     "true",
			wantPull: true,
		},
		{
			// Nothing can be read from either image -- `docker create` refuses
			// both, which an arm64 image the amd64 runner cannot create, a
			// daemon in a bad state or a runner out of space all look like. The
			// step has no answer, and "I could not read it" is not "it is the
			// same": it has to publish.
			name: "neither image can be read: docker create refuses it",
			config: map[string]map[string]string{
				"amd64": {"local": "sha256:aa", "published": "sha256:dd"},
				"arm64": {"local": "sha256:bb", "published": "sha256:ee"},
			},
			content:   sameContent,
			pullable:  map[string]bool{"amd64": true, "arm64": true},
			readFails: "create",
			want:      "false",
			wantPull:  true,
		},
		{
			name: "neither image can be read: docker cp finds no file",
			config: map[string]map[string]string{
				"amd64": {"local": "sha256:aa", "published": "sha256:dd"},
				"arm64": {"local": "sha256:bb", "published": "sha256:ee"},
			},
			content:   sameContent,
			pullable:  map[string]bool{"amd64": true, "arm64": true},
			readFails: "cp",
			want:      "false",
			wantPull:  true,
		},
		{
			name: "the published base is another tree's",
			config: map[string]map[string]string{
				"amd64": {"local": "sha256:aa", "published": "sha256:dd"},
				"arm64": {"local": "sha256:bb", "published": "sha256:ee"},
			},
			content: map[string]map[string]string{
				"amd64": {"local": "pins", "published": "other"},
				"arm64": {"local": "pins", "published": "other"},
			},
			pullable: map[string]bool{"amd64": true, "arm64": true},
			want:     "false",
			wantPull: true,
		},
		{
			name: "one architecture changed and the other did not",
			config: map[string]map[string]string{
				"amd64": {"local": "sha256:aa", "published": "sha256:aa"},
				"arm64": {"local": "sha256:bb", "published": "sha256:ee"},
			},
			content: map[string]map[string]string{
				"amd64": {"local": "pins", "published": "pins"},
				"arm64": {"local": "pins", "published": "other"},
			},
			pullable: map[string]bool{"amd64": true, "arm64": true},
			want:     "false",
			wantPull: true,
		},
		{
			name: "one architecture agrees by digest and the other by content",
			config: map[string]map[string]string{
				"amd64": {"local": "sha256:aa", "published": "sha256:aa"},
				"arm64": {"local": "sha256:bb", "published": "sha256:ee"},
			},
			content:  sameContent,
			pullable: map[string]bool{"amd64": true, "arm64": true},
			want:     "true",
			wantPull: true,
		},
		{
			name: "nothing is published under that tag yet",
			config: map[string]map[string]string{
				"amd64": {"local": "sha256:aa", "published": ""},
				"arm64": {"local": "sha256:bb", "published": ""},
			},
			content:  sameContent,
			pullable: map[string]bool{},
			want:     "false",
			wantPull: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "state")
			out := filepath.Join(state, "out")
			if err := os.MkdirAll(filepath.Join(state, "refs"), 0o755); err != nil {
				t.Fatal(err)
			}
			for arch, sides := range c.config {
				if digest := sides["local"]; digest != "" {
					writeFile(t, state, "local_id_"+arch, digest+"\n")
					writeFingerprintFixture(t, state, local, c.content[arch]["local"])
				}
				if digest := sides["published"]; digest != "" {
					writeFile(t, state, "published_id_"+arch, digest+"\n")
					if c.pullable[arch] {
						writeFile(t, state, "pullable_"+arch, "yes\n")
						writeFingerprintFixture(t, state, published(arch), c.content[arch]["published"])
						if c.readFails != "" {
							writeFile(t, state, c.readFails+"_fails", "yes\n")
						}
					}
				}
			}
			cmd := exec.Command("bash", step)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"FAKE_STATE="+state,
				"VERSION="+version,
				"GITHUB_OUTPUT="+out,
				"GITHUB_STEP_SUMMARY="+filepath.Join(state, "summary"),
			)
			if combined, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the unchanged step failed: %v\n%s", err, combined)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("the step wrote no $GITHUB_OUTPUT: %v", err)
			}
			if want := "unchanged=" + c.want + "\n"; string(got) != want {
				t.Errorf("the step decided %q, want %q", string(got), want)
			}
			calls, err := os.ReadFile(filepath.Join(state, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			if pulled := strings.Contains(string(calls), "pull --quiet"); pulled != c.wantPull {
				t.Errorf("the step pulled the published image: %v, want %v\ncalls:\n%s", pulled, c.wantPull, calls)
			}
		})
	}
}

// stepByID returns the step with id id, if any.
func stepByID(wf imagesWorkflow, job, id string) (workflowStep, bool) {
	for _, s := range wf.Jobs[job].Steps {
		if s.ID == id {
			return s, true
		}
	}
	return workflowStep{}, false
}

// writeFingerprintFixture writes the three files the fingerprint reads, all
// three carrying what the side is: two sides given the same string hash the
// same, and two given different ones cannot. The directory is named the way
// the stub's `tr '/:@' '___'` names it, so the two have to agree.
func writeFingerprintFixture(t *testing.T, state, ref, what string) {
	t.Helper()
	rel := filepath.Join("refs", strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(ref))
	writeFile(t, state, filepath.Join(rel, "base.json"), "{\"debian\":\""+what+"\"}\n")
	writeFile(t, state, filepath.Join(rel, "fugaro"), "binary of "+what+"\n")
	writeFile(t, state, filepath.Join(rel, "dpkg-status"), "Package: base-files\nVersion: "+what+"\n")
}

func writeGzip(t *testing.T, path string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte("an image archive the step only loads\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
