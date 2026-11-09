package imagesurvey

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

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
	// never be one no rule answers with.
	for _, kind := range kinds {
		if !slices.ContainsFunc(Signals(), func(s Signal) bool { return s.Kind == kind }) {
			t.Errorf("no rule answers with the kind %q", kind)
		}
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
// rather than failing the read. A read that could not be made is the same
// error as a file that is not there to this interface, so a repository whose
// credential can read nothing is a repository with no signals — the row the
// command reports as unread is decided by the opener, not here.
func TestSurveyReadAsksOnlyForTheFixedPaths(t *testing.T) {
	r := &stubReader{files: map[string]string{"Dockerfile": "FROM scratch\n"}, fail: errors.New("403")}
	findings, err := Read(context.Background(), r, "release")
	if err != nil {
		t.Fatalf("Read on a reader that refuses every path: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none", findings)
	}
	want := make([]string, 0, len(Signals()))
	refs := make([]string, 0, len(Signals()))
	for _, s := range Signals() {
		if seen := slices.Contains(want, s.Path); seen {
			continue
		}
		want = append(want, s.Path)
		refs = append(refs, "release")
	}
	if !slices.Equal(r.reads, want) {
		t.Errorf("Read asked for %v, want the fixed list %v", r.reads, want)
	}
	if !slices.Equal(r.refs, refs) {
		t.Errorf("Read used the refs %v, want %q for every path", r.refs, "release")
	}
}

// TestSurveyReadStopsOnACancelledContext: every other read failure is one
// path's, but a cancelled context is the caller's own and stops the read
// rather than answering a repository's signals from an empty tree.
func TestSurveyReadStopsOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &stubReader{files: map[string]string{"Dockerfile": "FROM scratch\n"}, fail: context.Canceled}
	if _, err := Read(ctx, r, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read on a cancelled context: err = %v, want context.Canceled", err)
	}
}
