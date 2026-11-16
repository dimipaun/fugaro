package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/imagesurvey"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// surveyProvider is one repository's file-backed provider with the reads it
// was asked for and the provider kind the command opened it as recorded: the
// survey's two claims about a repository — that it reads a fixed list of paths
// and that it reads them with the process environment's credential — are
// claims about what the opener was handed and what the provider was asked
// for, and this is where a test looks at both.
type surveyProvider struct {
	*fake.Provider
	kind string
	// cancel, when set, runs just before the first read of the fixed list: the
	// caller's own cancellation arriving mid-survey, which is the one failure
	// that is not the repository's.
	cancel func()

	mu    sync.Mutex
	paths []string
}

func (p *surveyProvider) Contents(ctx context.Context, path, ref string) ([]byte, error) {
	p.mu.Lock()
	first := len(p.paths) == 0
	p.paths = append(p.paths, path)
	p.mu.Unlock()
	if first && p.cancel != nil {
		p.cancel()
	}
	return p.Provider.Contents(ctx, path, ref)
}

func (p *surveyProvider) read() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.paths)
}

// surveyStub installs the survey's opener: the provider kind comes from the
// project config (so it is resolved for real) and the read itself is
// file-backed, which is the network no test has. A repository with no entry in
// files is one this machine holds no credential for: the opener refuses it,
// which is the row the report has to name rather than leave out. It answers
// the environment the command handed the opener, so a test can check that it
// is the process's own.
func surveyStub(t *testing.T, files map[string]map[string]string) (map[string]*surveyProvider, *[]string) {
	t.Helper()
	ps := map[string]*surveyProvider{}
	for repo, tree := range files {
		ps[repo] = &surveyProvider{Provider: &fake.Provider{Repo: repo, State: fake.State{Files: tree}}}
	}
	was := openSurveyProvider
	env := new([]string)
	openSurveyProvider = func(e []string, _ func(string)) (string, gitprov.Opener, error) {
		*env = e
		return "", func(_ context.Context, kind, repo string) (gitprov.Provider, []string, error) {
			p, ok := ps[repo]
			if !ok {
				return nil, nil, fmt.Errorf("no credential on this machine for %s", repo)
			}
			p.kind = kind
			return p, nil, nil
		}, nil
	}
	t.Cleanup(func() { openSurveyProvider = was })
	return ps, env
}

// surveyDoc runs `fugaro image survey --json` and decodes the document.
func surveyDoc(t *testing.T, args ...string) imageSurveyOut {
	t.Helper()
	out, _, err := execute(t, append([]string{"image", "survey", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var doc imageSurveyOut
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Version != imageSurveyDocVersion {
		t.Fatalf("version = %d, want %d", doc.Version, imageSurveyDocVersion)
	}
	return doc
}

// surveyRow is one repository's row of the document.
func surveyRow(t *testing.T, doc imageSurveyOut, repo string) imageSurveyRepo {
	t.Helper()
	for _, r := range doc.Repos {
		if r.Repo == repo {
			return r
		}
	}
	t.Fatalf("the document has no row for %s: %+v", repo, doc.Repos)
	return imageSurveyRepo{}
}

// surveySignals is a row's findings as "kind path value" lines, which is what
// a test compares against.
func surveySignals(r imageSurveyRepo) []string {
	out := make([]string, 0, len(r.Signals))
	for _, s := range r.Signals {
		out = append(out, strings.TrimSpace(s.Kind+" "+s.Path+" "+s.Value))
	}
	return out
}

// TestSurveyValuesAreValidated: every value the report carries came out of a
// rule's own regexp, and a file whose content is not one of those shapes
// contributes nothing of its own text. The repository below is one whose files
// are not what their names promise: a FROM line with a bidi override, a
// version file that is not a version, and a package.json whose engines range
// carries an escape sequence. None of the three may reach the report as a
// value, and none of the bytes may reach it at all (F22).
func TestSurveyValuesAreValidated(t *testing.T) {
	newCloudFixture(t)
	surveyStub(t, map[string]map[string]string{
		"acme/app": {
			"package-lock.json":  "{}",
			"package.json":       `{"engines": {"node": ">=20 <\u001b[31m"}}`,
			".nvmrc":             "20.11.0‮EVIL\n",
			"Dockerfile":         "FROM ‮ghcr.io/evil/image\n",
			"docker-compose.yml": "services:\n  db: { image: postgres:16 }\n  cache: { image: redis }\n",
			"go.mod":             "module example\n\ngo 1.23.4\n",
		},
	})

	out, _, err := execute(t, "image", "survey", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, raw := range []string{"‮", "\x1b", "EVIL", "evil"} {
		if strings.Contains(out, raw) {
			t.Errorf("the report carries %q, which is a file's own text:\n%s", raw, out)
		}
	}
	row := surveyRow(t, surveyDoc(t), "acme/app")
	if got, want := surveySignals(row), []string{
		"lockfile package-lock.json npm",
		"version go.mod 1.23.4",
		"dockerfile Dockerfile", // read, and its FROM line is not one this survey carries
		"compose docker-compose.yml postgres,redis",
	}; !slices.Equal(got, want) {
		t.Errorf("the row's signals are\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// surveyPathCount is how many distinct paths the survey reads, as a literal:
// the read the command makes is the whole of its F22 exposure, and a fixture
// list derived from the package would be green for a rule the package gained
// (internal/imagesurvey's surveyFixedPaths pins the same list from its own
// side, path by path).
const surveyPathCount = 30

// TestSurveyReadsOnlySignals: the survey reads the fixed list of paths and
// nothing else, at the process environment's credential, and the report
// carries the values the rules bounded rather than the files they came from.
// The repository below keeps a marker word in four files, three of them files
// no rule reads: if any of them reaches the report, the command is reading a
// repository's tree instead of its signals.
//
// Mutation (run, restored): read the repository's worktree (a clone, a tree
// walk) or print a file's bytes in the finding, and the marker appears in the
// read; the fixed-list assertion fails on the first path a walk reads.
func TestSurveyReadsOnlySignals(t *testing.T) {
	const marker = "MARKERONLY"
	t.Setenv("FUGARO_SURVEY_SENTINEL", "1")
	newCloudFixture(t)
	ps, env := surveyStub(t, map[string]map[string]string{
		"acme/app": {
			"Dockerfile":   "FROM node:20\nRUN echo " + marker + "\n",
			"fugaro.yaml":  "version: 1\nproject: aurora\n" + marker + "\n",
			"README.md":    marker + "\n",
			".env.example": marker + "=1\n",
		},
	})

	out, _, err := execute(t, "image", "survey")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, marker) {
		t.Errorf("the report carries a repository's own file content (%q):\n%s", marker, out)
	}
	if !strings.Contains(out, "node:20") {
		t.Errorf("the report does not carry the base image the Dockerfile names:\n%s", out)
	}

	// The list is pinned by imagesurvey's own literal (surveyFixedPaths), and
	// the count is named here too: a rule added for a path no fixture holds,
	// or one taken away, is red at one of the two ends without the other.
	want := imagesurvey.Paths()
	if len(want) != surveyPathCount {
		t.Errorf("the survey reads %d paths, want %d: a rule was added or taken away, and this test's fixtures hold none of the new one", len(want), surveyPathCount)
	}
	if got := ps["acme/app"].read(); !slices.Equal(got, want) {
		t.Errorf("the provider was asked for\n  %s\nwant the fixed list\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	// The credential is the process environment's own, not the project's
	// credential service (F22): the opener is handed the environment this
	// command runs in, with the sentinel the test set in it.
	if !slices.Contains(*env, "FUGARO_SURVEY_SENTINEL=1") {
		t.Errorf("the opener was handed %d variables, and not the process environment", len(*env))
	}
	if got := len(ps["acme/app"].State.Calls); got != 0 {
		t.Errorf("the survey made %d writing calls to the provider, want none", got)
	}
}

// TestSurveyBitbucketAndGitHub: the report is one row per repository the
// project config lists, each resolved to its own provider kind and read there
// — the two adapters the project's repositories are on (U6) — and a
// repository the survey cannot open is a row saying so rather than a
// repository left out or a command that fails.
func TestSurveyBitbucketAndGitHub(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "  acme/service: { provider: bitbucket }\n  acme/elsewhere: { provider: github }\n")
	ps, _ := surveyStub(t, map[string]map[string]string{
		"acme/app": {
			".nvmrc":                   "20.11.0\n",
			".github/workflows/ci.yml": "on: push\n",
		},
		"acme/service": {
			"poetry.lock":             "",
			".python-version":         "3.12\n",
			"bitbucket-pipelines.yml": "pipelines:\n",
		},
	})

	doc := surveyDoc(t)
	var names []string
	for _, r := range doc.Repos {
		names = append(names, r.Repo)
	}
	if want := []string{"acme/app", "acme/elsewhere", "acme/service"}; !slices.Equal(names, want) {
		t.Fatalf("the report lists %v, want %v", names, want)
	}
	if got, want := surveySignals(surveyRow(t, doc, "acme/app")), []string{
		"version .nvmrc 20.11.0",
		"ci .github/workflows/ci.yml github-actions",
	}; !slices.Equal(got, want) {
		t.Errorf("acme/app's signals are %v, want %v", got, want)
	}
	if got, want := surveySignals(surveyRow(t, doc, "acme/service")), []string{
		"lockfile poetry.lock poetry",
		"version .python-version 3.12",
		"ci bitbucket-pipelines.yml bitbucket",
	}; !slices.Equal(got, want) {
		t.Errorf("acme/service's signals are %v, want %v", got, want)
	}
	for repo, kind := range map[string]string{"acme/app": "github", "acme/service": "bitbucket"} {
		if got := surveyRow(t, doc, repo).Provider; got != kind {
			t.Errorf("%s was read as the %q provider, want %q", repo, got, kind)
		}
		if got := ps[repo].kind; got != kind {
			t.Errorf("%s was opened as the %q provider, want %q", repo, got, kind)
		}
	}
	// A repository whose opener refuses it is a row, not a hole: the reason is
	// on it and its signals list is empty.
	row := surveyRow(t, doc, "acme/elsewhere")
	if row.Unread == "" {
		t.Errorf("acme/elsewhere has no reason for its empty row: %+v", row)
	}
	if len(row.Signals) != 0 {
		t.Errorf("acme/elsewhere was read anyway: %+v", row.Signals)
	}
	if out, _, err := execute(t, "image", "survey"); err != nil || !strings.Contains(out, "unread:") {
		t.Errorf("the read does not say why a repository is unread (err %v):\n%s", err, out)
	}
}

// TestSurveyNothingReadIsNotNoSignals: a repository whose provider opened but
// whose every read came back empty-handed is a row carrying unread, not one
// reporting the empty signal list a readable repository with none of these
// files reports. ContentsReader answers one error for a file that is not there
// and for a read that could not be made, so the two would be byte-identical
// without the count: the row below is a repository that is not there (or one
// this machine's credential reads nothing out of), and the one beside it is a
// repository the survey read that holds none of its files — the second is what
// "no signals" is for.
func TestSurveyNothingReadIsNotNoSignals(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "  acme/gone: { provider: github }\n  acme/quiet: { provider: github }\n  acme/service: { provider: github }\n")
	surveyStub(t, map[string]map[string]string{
		// Every path refused: nothing at all was read.
		"acme/gone": {},
		// Read, and the one file it holds is not a version: the row has no
		// signals and no reason, which is what a readable repository with none
		// of the survey's files looks like.
		"acme/quiet": {".nvmrc": "not a version\n"},
		// Read, and its file names no service this survey carries, so the row
		// carries a signal with no value beside it.
		"acme/service": {"docker-compose.yml": "services:\n  mail: { image: mailhog }\n"},
	})

	doc := surveyDoc(t)
	gone := surveyRow(t, doc, "acme/gone")
	if gone.Unread == "" {
		t.Errorf("a repository not one of the survey's paths could be read from has no reason on its row: %+v", gone)
	}
	if !strings.Contains(gone.Unread, "not one of the") {
		t.Errorf("acme/gone's reason is %q, want the one that says nothing could be read", gone.Unread)
	}
	if len(gone.Signals) != 0 {
		t.Errorf("acme/gone carries signals anyway: %+v", gone.Signals)
	}
	quiet := surveyRow(t, doc, "acme/quiet")
	if quiet.Unread != "" {
		t.Errorf("a repository whose file was read carries a reason: %q", quiet.Unread)
	}
	if len(quiet.Signals) != 0 {
		t.Errorf("acme/quiet carries signals: %+v", quiet.Signals)
	}
	if got, want := surveySignals(surveyRow(t, doc, "acme/service")), []string{"compose docker-compose.yml"}; !slices.Equal(got, want) {
		t.Errorf("acme/service's signals are %v, want %v", got, want)
	}
	// The two rows must not read alike on the terminal either: the report says
	// why one is empty rather than printing the sentence the other one gets.
	out, _, err := execute(t, "image", "survey")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "unread: not one of the") {
		t.Errorf("the report does not say a repository nothing could be read from is unread:\n%s", out)
	}
	if !strings.Contains(out, "no signals at the paths the survey reads") {
		t.Errorf("the report does not say the repository that was read holds no signals:\n%s", out)
	}
}

// TestSurveySetupIsTheProjectOwnOrSaysWhyNot: the setup half of the document
// carries the three values a rerun of the skill compares against — the
// catalog, the repository-to-image map and the bake switch (F16, IS10's
// interface, D1901/D1906) — and it keeps the same distinction the repository
// rows keep. A project that published no map and bakes, a project whose node
// this machine cannot read and a project whose node says otherwise are one
// document without the reason: the defaults are byte-identical to a setting
// nobody wrote. The reason travels with them, so a reader is never told a
// project bakes when nothing was read.
func TestSurveySetupIsTheProjectOwnOrSaysWhyNot(t *testing.T) {
	f := newBudgetFixture(t, "")
	surveyStub(t, map[string]map[string]string{"acme/app": {"Dockerfile": "FROM node:20\n"}})

	// Nothing published at all: the defaults are the truth about the project —
	// an empty map and baking (D1901's default) — and the setup carries no
	// reason for them.
	doc := surveyDoc(t)
	if doc.Setup.Unread != "" {
		t.Errorf("a project that published no policy node is reported unread: %q", doc.Setup.Unread)
	}
	if len(doc.Setup.ImageMap) != 0 || !doc.Setup.BakeRepos {
		t.Errorf("setup = %+v, want the empty map and bake_repos true", doc.Setup)
	}

	// A node that was read: its own map and switch, and still no reason.
	f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	f.db.Set(projectpolicy.PathShared(), json.RawMessage(`{"version":5,"image_map":{"acme/app":"go"},"bake_repos":false}`))
	doc = surveyDoc(t)
	if doc.Setup.Unread != "" {
		t.Errorf("a policy node that was read is reported unread: %q", doc.Setup.Unread)
	}
	if got := doc.Setup.ImageMap["acme/app"]; got != "go" || doc.Setup.BakeRepos {
		t.Errorf("setup = %+v, want the node's own map and bake setting", doc.Setup)
	}

	// The node's map without a bake switch: absent means baking, and the
	// document reports that default rather than leaving the member out.
	f.db.Set(projectpolicy.PathShared(), json.RawMessage(`{"version":5,"image_map":{"acme/app":"go"}}`))
	doc = surveyDoc(t)
	if !doc.Setup.BakeRepos || doc.Setup.Unread != "" {
		t.Errorf("setup = %+v, want bake_repos true (a node that states no switch bakes) and no reason", doc.Setup)
	}

	// This machine's own config states both keys, and the database then refuses
	// the node: the two values are this machine's own rather than the project's,
	// which is exactly what the reason has to say beside them (localcfg's keep
	// rule, D1140).
	f.appendConfig(t, "image_map: {acme/app: local}\nbake_repos: false\n")
	f.db.DenyRead(projectpolicy.PathShared())
	doc = surveyDoc(t)
	if !strings.Contains(doc.Setup.Unread, "the project's published policy node could not be read") {
		t.Fatalf("setup.unread = %q, want the reason the node was not read", doc.Setup.Unread)
	}
	if !strings.Contains(doc.Setup.Unread, "are this machine's own rather than what this project published") {
		t.Errorf("setup.unread = %q, and a reader has to be told what the members are then", doc.Setup.Unread)
	}
	if got := doc.Setup.ImageMap["acme/app"]; got != "local" || doc.Setup.BakeRepos {
		t.Errorf("an unread setup answers %+v, want this machine's own map and switch", doc.Setup)
	}
	// And the terminal says it too, before the members it is about.
	out, _, err := execute(t, "image", "survey")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "unread: the project's published policy node could not be read") {
		t.Errorf("the report does not say the setup was not read:\n%s", out)
	}
	if !strings.Contains(out, "bake_repos: no") || !strings.Contains(out, "local") {
		t.Errorf("the report does not carry this machine's own map and switch:\n%s", out)
	}
}

// executeSurveyCtx runs the command tree with a context the test controls.
func executeSurveyCtx(t *testing.T, ctx context.Context, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetContext(ctx)
	cmd.SetArgs(NormalizeArgs(args))
	return out.String(), cmd.Execute()
}

// TestSurveyStopsOnACancelledContext: the survey the caller stops — Ctrl-C at
// a terminal — is the caller's own cancellation, not a repository nobody could
// read. It stops the command with the reason instead of marking rows unread,
// working through the remaining repositories and exiting 0 with a
// full-looking report of a read that never finished.
func TestSurveyStopsOnACancelledContext(t *testing.T) {
	newCloudFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ps, _ := surveyStub(t, map[string]map[string]string{
		"acme/app":  {"Dockerfile": "FROM node:20\n"},
		"acme/zeta": {"Dockerfile": "FROM node:20\n"},
	})
	// The cancellation arrives on the first read, as it does when the owner
	// stops the survey with a repository half read.
	ps["acme/app"].cancel = cancel

	out, err := executeSurveyCtx(t, ctx, "image", "survey")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled: a stopped survey is not a report of anything", err)
	}
	if out != "" {
		t.Errorf("the command printed a report of a read it did not finish:\n%s", out)
	}
	if got := len(ps["acme/app"].read()); got != 1 {
		t.Errorf("the cancelled repository was asked for %d paths, want the one read the stop interrupted", got)
	}
	if got := len(ps["acme/zeta"].read()); got != 0 {
		t.Errorf("the survey read %d paths of a repository after the context was done, want none", got)
	}

	// The same cancellation before any read at all: the command still stops
	// with the reason rather than surveying from a context nobody is waiting
	// on.
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	ps["acme/app"].cancel = nil
	if out, err := executeSurveyCtx(t, cancelled, "image", "survey", "--json"); !errors.Is(err, context.Canceled) {
		t.Errorf("a survey started on a done context: err = %v, want context.Canceled\n%s", err, out)
	}
}

// TestSurveyGateRowReadOnly: the survey is a report, so it belongs to no row
// of the managed gate's set and needs none — it writes nothing, and it runs
// unchanged in the coding agent's session the gate refuses every owner-level
// write in. What its row would have said is checked here instead, in the
// three things that make the claim true: the command carries neither of the
// gate's two flags, it carries no gate at all (it is refused nothing with the
// marker set and no terminal), and it writes nothing anywhere — the project's
// bucket and the provider are as it found them.
func TestSurveyGateRowReadOnly(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	f := newCloudFixture(t)
	before := bucketTree(t, filepath.Join(f.dir, "runs"))
	ps, _ := surveyStub(t, map[string]map[string]string{
		"acme/app": {"Dockerfile": "FROM node:20\n"},
	})

	cmd := newImageCmd()
	survey, _, err := cmd.Find([]string{"survey"})
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"yes", "ask"} {
		if survey.Flags().Lookup(flag) != nil {
			t.Errorf("image survey carries --%s, and the gate's commands are the ones that carry it", flag)
		}
	}

	out, _, err := execute(t, "image", "survey")
	if err != nil {
		t.Fatalf("a coding agent's session could not run the survey: %v\n%s", err, out)
	}
	if !strings.Contains(out, "acme/app") {
		t.Errorf("the report does not carry the project's repository:\n%s", out)
	}
	if got := bucketTree(t, filepath.Join(f.dir, "runs")); !slices.Equal(got, before) {
		t.Errorf("the survey changed the project's bucket:\n  before %v\n  after  %v", before, got)
	}
	if got := len(ps["acme/app"].State.Calls); got != 0 {
		t.Errorf("the survey made %d writing calls to the provider, want none", got)
	}
}

// bucketTree is every object under dir, so a test can say a command wrote
// nothing.
func bucketTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, strings.TrimPrefix(path, dir))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// TestSurveyJSONMatchesSchema validates the document the command writes
// against schemas/image-survey.schema.json, the contract a consumer reads it
// by (D784).
func TestSurveyJSONMatchesSchema(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "  acme/service: { provider: bitbucket }\n")
	surveyStub(t, map[string]map[string]string{
		"acme/app": {
			"Dockerfile":         "FROM node:20\n",
			"docker-compose.yml": "services:\n  db: { image: postgres }\n",
		},
		"acme/service": {".nvmrc": "20\n"},
	})

	out, _, err := execute(t, "image", "survey", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "image-survey.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/image-survey.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the document does not match schemas/image-survey.schema.json: %v\n%s", err, out)
	}

	// The document is only worth validating while it carries the members the
	// schema closes over: a fixture that stopped producing a repository row, a
	// signal and the setup would leave those branches unchecked and still
	// green.
	var decoded struct {
		Version int `json:"version"`
		Repos   []struct {
			Repo    string `json:"repo"`
			Signals []struct {
				Kind string `json:"kind"`
			} `json:"signals"`
		} `json:"repos"`
		Setup struct {
			Catalog   []any             `json:"catalog"`
			ImageMap  map[string]string `json:"image_map"`
			BakeRepos *bool             `json:"bake_repos"`
		} `json:"setup"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != 1 {
		t.Errorf("version = %d, and the document's first version is 1 (D784)", decoded.Version)
	}
	if len(decoded.Repos) < 2 {
		t.Errorf("the document carries %d repository rows, and the fixture lists two", len(decoded.Repos))
	}
	kinds := map[string]bool{}
	for _, r := range decoded.Repos {
		for _, s := range r.Signals {
			kinds[s.Kind] = true
		}
	}
	for _, want := range []string{imagesurvey.KindDockerfile, imagesurvey.KindCompose, imagesurvey.KindVersion} {
		if !kinds[want] {
			t.Errorf("no row carries a %q signal, so the schema's enum branch for it is unchecked", want)
		}
	}
	// A project that has published no catalog is the document's own empty
	// catalog, which is what the schema's array branch is validated on here —
	// and the map and the switch are the members the setup's own branches are
	// validated on, so a fixture that stopped producing them cannot leave those
	// unchecked and still be green.
	if decoded.Setup.Catalog == nil {
		t.Errorf("setup = %+v, want the catalog member the schema closes over", decoded.Setup)
	}
	if decoded.Setup.ImageMap == nil {
		t.Errorf("setup = %+v, want the image_map member the schema closes over", decoded.Setup)
	}
	if decoded.Setup.BakeRepos == nil {
		t.Errorf("setup = %+v, want the bake_repos member the schema closes over", decoded.Setup)
	}
}
