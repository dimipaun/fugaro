package imagesurvey

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// surveyFixedPaths is the whole read, written out by hand: the paths the
// survey is allowed to ask a repository for, once each and in the rules' order.
//
// It is here, as a literal, because every other check of this list is derived
// from the list it is checking — Signals and Paths read the same rules slice
// the fixtures build their expectation from — so a rule added for a path no
// fixture holds, or one whose content no assertion looks at, would land green.
// The widening F22 forbids is exactly that rule: this literal is what makes it
// red here, at the one place that knows what the list is.
var surveyFixedPaths = []string{
	"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "go.sum",
	"Cargo.lock", "poetry.lock", "requirements.txt", "Gemfile.lock", "composer.lock",
	".nvmrc", ".python-version", ".tool-versions", "go.mod", "rust-toolchain.toml", "package.json",
	"Dockerfile",
	".github/workflows/ci.yml", ".gitlab-ci.yml", "bitbucket-pipelines.yml", "Jenkinsfile",
	".circleci/config.yml", "azure-pipelines.yml",
	"docker-compose.yml", "docker-compose.yaml", "compose.yaml", "compose.yml",
	"pom.xml", "build.gradle", "build.gradle.kts",
}

// surveyKindPaths is how many rules judge each kind: the entries of Signals,
// not the distinct paths, so the two kinds that share a file (package.json,
// requirements.txt) count twice. A kind gaining a path, or losing one, is red
// here.
var surveyKindPaths = map[string]int{
	KindLockfile:       10,
	KindVersion:        6,
	KindDockerfile:     1,
	KindCI:             6,
	KindCompose:        4,
	KindTestcontainers: 5,
}

// TestSurveySignalsAreFixed is the package's own contract: the list is a set
// of literal repository-relative paths, one per kind, with no wildcard and no
// traversal. gitprov.ContentsReader lists nothing, so a path that is not a
// literal is a path that could only be reached by walking a repository's tree
// — the discovery this package exists to not do (F22).
func TestSurveySignalsAreFixed(t *testing.T) {
	kinds := []string{KindLockfile, KindVersion, KindDockerfile, KindCI, KindCompose, KindTestcontainers}
	// A path may carry two kinds — package.json is a version file and a
	// Testcontainers build file, requirements.txt a lockfile and one too — so
	// what is listed once is a kind and a path together.
	seen := map[Signal]bool{}
	for _, s := range Signals() {
		if !slices.Contains(kinds, s.Kind) {
			t.Errorf("%s: kind %q is not one of the document's kinds %v", s.Path, s.Kind, kinds)
		}
		if seen[s] {
			t.Errorf("%s is in the list twice as %q", s.Path, s.Kind)
		}
		seen[s] = true
		if s.Path == "" || strings.HasPrefix(s.Path, "/") || strings.Contains(s.Path, "..") ||
			strings.ContainsAny(s.Path, "*?[]") || strings.Contains(s.Path, "\\") {
			t.Errorf("%q is not a fixed repository-relative path", s.Path)
		}
	}
	// Every kind has at least one path, so a kind the document prints can
	// never be one no rule answers with, and no kind gains or loses a path
	// without surveyKindPaths being written again.
	for _, kind := range kinds {
		n := 0
		for _, s := range Signals() {
			if s.Kind == kind {
				n++
			}
		}
		if n == 0 {
			t.Errorf("no rule answers with the kind %q", kind)
		}
		if want, ok := surveyKindPaths[kind]; !ok {
			t.Errorf("the kind %q has no count in surveyKindPaths, so a rule for it is unpinned", kind)
		} else if n != want {
			t.Errorf("%d rules answer with the kind %q, want %d: a path was added to or taken from this kind", n, kind, want)
		}
	}
	for kind := range surveyKindPaths {
		if !slices.Contains(kinds, kind) {
			t.Errorf("surveyKindPaths counts the kind %q, which is not one of the document's", kind)
		}
	}
	// The read itself, path by path, against the literal list: this is what a
	// new rule for a path no fixture holds has to get past.
	if got := Paths(); !slices.Equal(got, surveyFixedPaths) {
		t.Errorf("Paths() = %v, want the fixed list %v", got, surveyFixedPaths)
	}
	// Signals answers a copy: a caller that sorts or truncates it does not
	// change what the survey reads.
	got := Signals()
	got[0].Path = "changed"
	if Signals()[0].Path == "changed" {
		t.Error("Signals answers the rules' own slice, so a caller can change what the survey reads")
	}
}

// TestSurveyValuesAreBoundByShape: every value a rule answers with is either
// this package's own constant or a token of that rule's own regexp — never a
// line of someone else's file. The files here are the shapes a hostile or
// merely odd repository has: a FROM line with a bidi override in it, a
// version file whose content is not a version, a package.json whose engines
// range carries an escape sequence and a compose file naming services this
// survey has no starter for.
func TestSurveyValuesAreBoundByShape(t *testing.T) {
	findings := Findings([]File{
		{Path: "Dockerfile", Data: []byte("FROM node:20\nRUN true\n")},
		{Path: ".nvmrc", Data: []byte("20.11.0‮EVIL\n")},
		{Path: "package.json", Data: []byte(`{"engines": {"node": ">=20 <\u001b[31m"}}`)},
		{Path: "docker-compose.yml", Data: []byte("services:\n  mail: { image: mailhog }\n")},
		{Path: "go.mod", Data: []byte("module example\n\ngo 1.23.4\n")},
	})
	byPath := map[string]Finding{}
	for _, f := range findings {
		byPath[f.Path] = f
	}

	if got := byPath["Dockerfile"]; got.Kind != KindDockerfile || got.Value != "node:20" {
		t.Errorf("the Dockerfile answered %+v, want the base image its FROM line names", got)
	}
	// A version file that holds something else answers no version at all: a
	// token nobody bounded is not a signal, and the line itself is not one
	// either.
	if got, ok := byPath[".nvmrc"]; ok {
		t.Errorf(".nvmrc answered %+v, and its content is not a version", got)
	}
	if got, ok := byPath["package.json"]; ok {
		t.Errorf("package.json answered %+v, and its engines range carries an escape", got)
	}
	// A compose file naming a service Fugaro has no starter for is the file's
	// own presence with no service: the word never reaches the report.
	if got := byPath["docker-compose.yml"]; got.Kind != KindCompose || got.Value != "" {
		t.Errorf("the compose file answered %+v, want its presence and no service", got)
	}
	if got := byPath["go.mod"]; got.Value != "1.23.4" {
		t.Errorf("go.mod answered %+v, want the go directive's version", got)
	}
	for _, f := range findings {
		if strings.ContainsAny(f.Value, "‮\u001b") {
			t.Errorf("%s: the value %q carries a control character", f.Path, f.Value)
		}
	}
}

// TestSurveyValuesCarryPrintableText: whatever a rule answers with leaves this
// package through pluginwire.Printable, which is the one net under every rule
// (F22) — a rule that bounded its value by shape but left an escape in it is
// still escaped on the way out, so the net is not a promise each rule keeps
// for itself.
//
// The rule below is injected for this test alone: every rule of the real list
// bounds its value at the regexp, so no file a test can write would reach that
// net, and a net no test reaches is a net the next rule could forget.
func TestSurveyValuesCarryPrintableText(t *testing.T) {
	was := rules
	rules = append(slices.Clone(rules), rule{KindVersion, "hostile", func([]byte) (string, bool) {
		return "x\u001b[31my", true
	}})
	t.Cleanup(func() { rules = was })

	findings := Findings([]File{{Path: "hostile", Data: []byte("anything")}})
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want the injected rule's own", findings)
	}
	if got, want := findings[0].Value, `x\u001b[31my`; got != want {
		t.Errorf("value = %q, want %q: a value leaves this package printable or not at all", got, want)
	}
}

// stubReader is a ContentsReader with a fixed tree that records the paths and
// refs it was asked for.
type stubReader struct {
	files map[string]string
	fail  error
	reads []string
	refs  []string
}

func (r *stubReader) Contents(_ context.Context, path, ref string) ([]byte, error) {
	r.reads = append(r.reads, path)
	r.refs = append(r.refs, ref)
	if r.fail != nil {
		return nil, r.fail
	}
	data, ok := r.files[path]
	if !ok {
		return nil, errors.New("not there")
	}
	return []byte(data), nil
}

var _ gitprov.ContentsReader = (*stubReader)(nil)

// TestSurveyReadAsksOnlyForTheFixedPaths: Read asks for each fixed path once,
// at the ref it was given, and a path that is not there contributes nothing
// rather than failing the read. The count it answers with is how many paths
// were read, not how many signals were found, and a reader that refuses every
// path is the zero the caller has to tell apart from a repository whose files
// were read and name nothing.
func TestSurveyReadAsksOnlyForTheFixedPaths(t *testing.T) {
	r := &stubReader{files: map[string]string{"Dockerfile": "FROM scratch\n"}, fail: errors.New("403")}
	findings, read, err := Read(context.Background(), r, "release")
	if err != nil {
		t.Fatalf("Read on a reader that refuses every path: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none", findings)
	}
	if read != 0 {
		t.Errorf("read = %d, want 0: not one path answered, and that is not the same as a repository with no signals", read)
	}
	refs := make([]string, 0, len(surveyFixedPaths))
	for range surveyFixedPaths {
		refs = append(refs, "release")
	}
	if !slices.Equal(r.reads, surveyFixedPaths) {
		t.Errorf("Read asked for %v, want the fixed list %v", r.reads, surveyFixedPaths)
	}
	if !slices.Equal(r.refs, refs) {
		t.Errorf("Read used the refs %v, want %q for every path", r.refs, "release")
	}
	if len(r.reads) != len(refs) {
		t.Errorf("Read made %d reads, want one per fixed path (%d)", len(r.reads), len(refs))
	}
}

// TestSurveyReadCountsWhatItRead: a repository some of whose paths answer is a
// read repository, however few of them carry a signal. The count is what makes
// the two empty reports different, so it counts reads and not findings.
func TestSurveyReadCountsWhatItRead(t *testing.T) {
	r := &stubReader{files: map[string]string{
		"Dockerfile": "FROM scratch\n",
		// A file that is there and names none of this package's signals: it is
		// read, and it contributes no finding.
		"docker-compose.yml": "services:\n  mail: { image: mailhog }\n",
	}}
	findings, read, err := Read(context.Background(), r, "")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read != 2 {
		t.Errorf("read = %d, want 2: two of the fixed paths answered", read)
	}
	if len(findings) != 2 {
		t.Errorf("findings = %+v, want the two files' own", findings)
	}
	if got := findings[1]; got.Kind != KindCompose || got.Value != "" {
		t.Errorf("the compose file answered %+v, want its presence and no service", got)
	}
}

// TestSurveyReadStopsOnACancelledContext: every other read failure is one
// path's, but a cancelled context is the caller's own and stops the read
// rather than answering a repository's signals from an empty tree.
func TestSurveyReadStopsOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &stubReader{files: map[string]string{"Dockerfile": "FROM scratch\n"}, fail: context.Canceled}
	if _, _, err := Read(ctx, r, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read on a cancelled context: err = %v, want context.Canceled", err)
	}
}
