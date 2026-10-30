package images_test

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// eeBuildImages are the images D524 gives the tagged binary: the job image,
// whose own fugaro runs the governance hooks, and the history image, whose
// coordinator sweep would otherwise hit the core stub. Every other
// images/*/Dockerfile builds the core.
var eeBuildImages = []string{"base", "history"}

// coreImages are the images D524 keeps untagged: the credentials service
// serves credentials and runs no coordination, report or governance, so it
// carries no ee/ code and its licence label stays MIT and true.
var coreImages = []string{"credentials"}

// runImages is every image a run starts on the installation's behalf; the
// key-material scan below reads all three.
var runImages = append(slices.Clone(eeBuildImages), coreImages...)

func readDockerfile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(name, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestBaseAndHistoryBuildFugaroWithTheEETag: the base and history images
// compile the tagged binary -- D524, design section 8 -- which means both the
// ee tag on the fugaro build and ee/ in the build context the COPY reads it
// from (F16b).
func TestBaseAndHistoryBuildFugaroWithTheEETag(t *testing.T) {
	for _, name := range eeBuildImages {
		t.Run(name, func(t *testing.T) {
			for _, gap := range dockerfileBuildTagGaps(name, readDockerfile(t, name)) {
				t.Error(gap)
			}
		})
	}
}

// TestCredentialsImageBuildsWithoutTheEETag is the other half of D524: the
// credentials image publishes no compiled form of ee/ and sets no tag, so its
// MIT label is true.
func TestCredentialsImageBuildsWithoutTheEETag(t *testing.T) {
	for _, name := range coreImages {
		t.Run(name, func(t *testing.T) {
			for _, gap := range dockerfileBuildTagGaps(name, readDockerfile(t, name)) {
				t.Error(gap)
			}
		})
	}
}

// TestBuildTagScanCatchesPlants keeps the scan above from being vacuous: each
// plant is a real Dockerfile with one edit, and the scan has to refuse it.
// The ENV/ARG plants are the finding E3.2's review deferred here: the
// licence-label gate reads a literal `-tags ee` on the `go build` line alone,
// so an image that sets the tag through GOFLAGS or an ARG would carry ee/ code
// under an MIT label unless something refuses the spelling itself.
func TestBuildTagScanCatchesPlants(t *testing.T) {
	buildRun := "RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \\\n"

	insertBefore := func(t *testing.T, body, anchor, insert string) string {
		t.Helper()
		i := strings.Index(body, anchor)
		if i < 0 {
			t.Fatalf("the Dockerfile has no %q to plant against", strings.TrimSpace(anchor))
		}
		return body[:i] + insert + body[i:]
	}
	replaceOnce := func(t *testing.T, body, old, new string) string {
		t.Helper()
		if !strings.Contains(body, old) {
			t.Fatalf("the Dockerfile has no %q to plant against", old)
		}
		return strings.Replace(body, old, new, 1)
	}
	// untag takes the literal tag off the fugaro build, leaving everything
	// else untouched.
	untag := func(t *testing.T, body string) string {
		t.Helper()
		return replaceOnce(t, body, " -tags ee ", " ")
	}

	// scan writes the planted bodies as images/<name>/Dockerfile and returns
	// the scan's verdict, so a plant is judged the way the tree is.
	scan := func(t *testing.T, bodies map[string]string) []string {
		t.Helper()
		root := t.TempDir()
		for _, name := range slices.Sorted(maps.Keys(bodies)) {
			dir := filepath.Join(root, "images", name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(bodies[name]), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return imageBuildTagGaps(t, root)
	}
	refused := func(t *testing.T, name string, gaps []string, want string) {
		t.Helper()
		if len(gaps) == 0 {
			t.Fatalf("%s reports no gap: an image whose build tag D524 forbids would ship", name)
		}
		if !strings.Contains(strings.Join(gaps, "\n"), want) {
			t.Errorf("%s reports %v, and no gap names %q", name, gaps, want)
		}
	}

	// The positive control: the tree's own bodies are accepted, so a scan
	// that refuses everything cannot pass the plants below.
	t.Run("the real bodies are accepted", func(t *testing.T) {
		bodies := map[string]string{}
		for _, name := range runImages {
			bodies[name] = readDockerfile(t, name)
		}
		if gaps := scan(t, bodies); len(gaps) > 0 {
			t.Errorf("the tree's own Dockerfiles report gaps: %v", gaps)
		}
	})

	t.Run("a base without the tag", func(t *testing.T) {
		body := untag(t, readDockerfile(t, "base"))
		refused(t, "an untagged base image", scan(t, map[string]string{"base": body}), "without the ee tag")
	})

	t.Run("a tagged build with no COPY ee", func(t *testing.T) {
		body := replaceOnce(t, readDockerfile(t, "base"), "COPY ee ./ee\n", "")
		refused(t, "a tagged build with no ee/ in the context", scan(t, map[string]string{"base": body}), "without copying ee/")
	})

	t.Run("a tagged credentials image", func(t *testing.T) {
		for _, name := range coreImages {
			body := replaceOnce(t, readDockerfile(t, name), "go build -trimpath", "go build -tags ee -trimpath")
			refused(t, "a credentials image built with the ee tag", scan(t, map[string]string{name: body}), "keeps this image core")
		}
	})

	t.Run("a tag set through ENV GOFLAGS", func(t *testing.T) {
		for _, name := range runImages {
			body := insertBefore(t, readDockerfile(t, name), buildRun, "ENV GOFLAGS=-tags=ee\n")
			refused(t, "a "+name+" image setting the tag through ENV GOFLAGS", scan(t, map[string]string{name: body}), "GOFLAGS")
		}
	})

	t.Run("a tag supplied by an ARG", func(t *testing.T) {
		for _, name := range runImages {
			body := readDockerfile(t, name)
			if slices.Contains(eeBuildImages, name) {
				body = untag(t, body)
			}
			body = replaceOnce(t, body, "go build -trimpath", "go build -tags $FUGARO_TAGS -trimpath")
			body = insertBefore(t, body, buildRun, "ARG FUGARO_TAGS=ee\n")
			refused(t, "a "+name+" image taking the tag from an ARG", scan(t, map[string]string{name: body}), "$FUGARO_TAGS")
		}
	})
}

// TestRunImagesCarryNoKey: no key and no secret ever enters a run image (R6,
// G9). Under T27 (b) a job needs no key at all, and when enforcement ships the
// entitlement travels in the task as a claim; a COPY or an ARG naming one is
// how it would come back.
func TestRunImagesCarryNoKey(t *testing.T) {
	named := regexp.MustCompile(`(?i)(^|[^a-z])(key|secret|token|password|passwd|credential)s?([^a-z]|$)`)
	for _, name := range runImages {
		t.Run(name, func(t *testing.T) {
			for _, d := range dockerfileDirectives(readDockerfile(t, name)) {
				fields := strings.Fields(d)
				switch fields[0] {
				case "ARG":
					for _, w := range fields[1:] {
						if named.MatchString(w) {
							t.Errorf("ARG %s reads key material into the image", w)
						}
					}
				case "COPY":
					for _, w := range copySources(fields) {
						if named.MatchString(w) {
							t.Errorf("COPY %s puts key material in the image", w)
						}
					}
				}
			}
		})
	}
}

// TestBuildContextAdmitsEE: the build context is the whole repository filtered
// by the root .dockerignore, and the base and history builds COPY ee ./ee
// (F16b). The list is pinned in full, so admitting ee/ cannot quietly widen the
// context past the exclusions the images already depend on.
func TestBuildContextAdmitsEE(t *testing.T) {
	b, err := os.ReadFile("../.dockerignore")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		lines = append(lines, l)
	}
	want := []string{
		"*",
		"!go.mod",
		"!go.sum",
		"!cmd/",
		"!internal/",
		"!ee/",
		"!images/",
		"!deploy/terraform/",
		"deploy/terraform/**/.terraform/",
		"internal/**/testdata/",
		"ee/**/testdata/",
	}
	if !slices.Equal(lines, want) {
		t.Errorf("the root .dockerignore is\n%v\nwant\n%v", lines, want)
	}
	if !slices.Contains(lines, "!ee/") {
		t.Error("the build context does not admit ee/, so the base and history builds' COPY ee ./ee has nothing to read")
	}
	for _, keep := range []string{"internal/**/testdata/", "ee/**/testdata/"} {
		if !slices.Contains(lines, keep) {
			t.Errorf("the build context no longer excludes %s: test data would be copied into the images", keep)
		}
	}
}

// ociLabels is a LABEL line whose every key is an org.opencontainers.image.*
// one. history_test.go and credentials_test.go read their final stages with it:
// the licence label F16c requires is the one thing a run image's config may
// carry, and a key outside the OCI set is a fact nobody pinned.
var ociLabels = regexp.MustCompile(`^LABEL( org\.opencontainers\.image\.[a-z.]+=("[^"]*"|\S+))+$`)

func onlyOCILabels(line string) bool {
	return ociLabels.MatchString(line)
}

// dockerfileDirectives returns a Dockerfile's instructions with their
// continuation lines joined and their comments dropped, so a caller reads a
// directive the way the builder does.
func dockerfileDirectives(body string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(body, "\\\n", " "), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// copySources returns the sources of a COPY instruction's fields, without the
// destination and the -- flags a COPY may carry.
func copySources(fields []string) []string {
	if len(fields) < 3 {
		return nil
	}
	var sources []string
	for _, f := range fields[1 : len(fields)-1] {
		if !strings.HasPrefix(f, "--") {
			sources = append(sources, f)
		}
	}
	return sources
}

// tagUse is what a Dockerfile's directives say about the ee build tag: the tag
// the licence-label gate can read (a literal `-tags ee` on the `go build`
// line), and every directive that sets a -tags flag anywhere else -- an ENV or
// an ARG carrying GOFLAGS, or an argument the shell expands -- where that gate
// cannot see it.
type tagUse struct {
	literalEE bool
	hidden    []string
}

// readTagUse is the one reader both the scan and its plants go through, so a
// spelling cannot be refused in one and accepted in the other.
func readTagUse(directives []string) tagUse {
	var use tagUse
	for _, d := range directives {
		fields := strings.Fields(d)
		isBuild := strings.Contains(d, "go build")
		for i := 0; i < len(fields); i++ {
			f := fields[i]
			var value string
			switch {
			// GOFLAGS=-tags=ee is one field, so the flag is looked for
			// anywhere in one rather than at its start.
			case strings.Contains(f, "-tags="):
				value = f[strings.Index(f, "-tags=")+len("-tags="):]
			case f == "-tags" && i+1 < len(fields):
				value = fields[i+1]
				i++
			default:
				continue
			}
			if isBuild && value == "ee" {
				use.literalEE = true
				continue
			}
			use.hidden = append(use.hidden, d)
		}
	}
	return use
}

// copiesEE reports whether the Dockerfile copies ee/ into the build: without
// it the tag compiles nothing (F16b).
func copiesEE(directives []string) bool {
	for _, d := range directives {
		fields := strings.Fields(d)
		if fields[0] != "COPY" {
			continue
		}
		for _, src := range copySources(fields) {
			if src == "ee" || strings.HasPrefix(src, "ee/") {
				return true
			}
		}
	}
	return false
}

// dockerfileBuildTagGaps is the scan: D524's rule for one image, read off the
// image's own name rather than off a tag the Dockerfile happens to carry.
func dockerfileBuildTagGaps(name, body string) []string {
	directives := dockerfileDirectives(body)
	use := readTagUse(directives)
	var gaps []string
	switch {
	case slices.Contains(eeBuildImages, name):
		if !use.literalEE {
			gaps = append(gaps, fmt.Sprintf("images/%s/Dockerfile builds fugaro without the ee tag this image needs (D524); the go build line has to pass a literal -tags ee", name))
		}
		if !copiesEE(directives) {
			gaps = append(gaps, fmt.Sprintf("images/%s/Dockerfile builds the tagged binary without copying ee/ into the context (F16b), so the tag compiles nothing", name))
		}
	case slices.Contains(coreImages, name):
		if use.literalEE {
			gaps = append(gaps, fmt.Sprintf("images/%s/Dockerfile builds fugaro with the ee tag; D524 keeps this image core, with no ee/ code and an MIT licence label", name))
		}
		if copiesEE(directives) {
			gaps = append(gaps, fmt.Sprintf("images/%s/Dockerfile copies ee/ into the image, which D524 keeps core", name))
		}
	default:
		gaps = append(gaps, fmt.Sprintf("images/%s/Dockerfile is in neither the tagged set %v nor the core set %v: D524 decides which images carry the ee build", name, eeBuildImages, coreImages))
	}
	for _, hidden := range use.hidden {
		gaps = append(gaps, fmt.Sprintf("images/%s/Dockerfile sets a -tags flag the licence-label gate cannot read (%s): the tag has to be a literal -tags ee on the go build line, never through GOFLAGS, an ARG or a shell variable", name, hidden))
	}
	return gaps
}

// imageBuildTagGaps runs the scan over every images/*/Dockerfile of the tree at
// root. The list comes from the tree, so an image added later is judged too.
func imageBuildTagGaps(t *testing.T, root string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "images", "*", "Dockerfile"))
	if err != nil {
		t.Fatalf("globbing the image Dockerfiles under %s: %v", root, err)
	}
	slices.Sort(paths)
	var gaps []string
	for _, path := range paths {
		name := filepath.Base(filepath.Dir(path))
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		gaps = append(gaps, dockerfileBuildTagGaps(name, string(body))...)
	}
	return gaps
}
