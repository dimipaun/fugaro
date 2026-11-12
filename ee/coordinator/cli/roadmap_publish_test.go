// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// This file is P9c: the command that reads a plan's graph out of a checkout and
// puts it into the roadmap record through the front door.
//
// Every test runs in a real git checkout — a temporary directory with an
// origin and one commit, which is what the command's provenance and its
// "what is published is what is committed" rule are read against. The front
// door is P9f's fake, so what these tests assert is what the CLI put on the
// wire and what it made of the answer.
//
// The one property that is not a behaviour is
// TestPublishNeverWritesTheDatabaseItself: the command reads a person's working
// tree, which is exactly where a shortcut past the front door would look
// tempting, so the file is held to the same rule as the rest of the group.

// publishGraph is a schema-3 graph with every field the trim has to decide
// about: a contract node carrying a stub and a size, a second node whose run
// the record does not hold, a target version, and an edge with a reason. What
// crosses the wire is the shape P9a holds; the footprint, the run, the stub
// list and the edge's reason are the artifact's and stay in the checkout.
const publishGraph = `{
  "schema_version": 3,
  "plan": "docs/plans/2026-11-12-the-plan.md",
  "stream": "cli",
  "nodes": [
    {"id": "1", "title": "the base", "run": null, "version": "0.24", "kind": "contract", "size": 3,
     "stubs": ["internal/base/stub.go"],
     "footprint": {"prediction": true, "paths": ["internal/base/stub.go", "internal/base/types.go"]}},
    {"id": "2", "title": "the dependent", "run": "the-run-1", "version": null,
     "footprint": {"prediction": true, "paths": ["internal/dep/**"]}}
  ],
  "edges": [
    {"task": "2", "needs": "1", "reason": "the dependent is built against the base"}
  ]
}`

// publishCatalogue is a release catalogue of the shape internal/streams reads:
// the three streams, each one name past its first, so the import has an order
// and a name to compute rather than a version to copy.
const publishCatalogue = `streams:
  - name: cli
    theme: animals
    names: [Aardvark, Badger, Camel]
    current: "0.11.0"
    next: Camel
    tag_prefix: v
  - name: console
    theme: fruits
    names: [Cherry, Mango, Melon]
    current: "0.111.0"
    next: Melon
    tag_prefix: console-v
  - name: mobile
    theme: birds
    names: [Falcon, Pelican, Robin]
    current: "0.201.0"
    next: Robin
    tag_prefix: mobile-v
`

// planDoc and planArtifact are the two files a plan is: the document and the
// graph beside it. Both are written and committed by publishCheckout, because a
// publish reads what the commit holds.
const (
	planDoc      = "docs/plans/2026-11-12-the-plan.md"
	planArtifact = "docs/plans/2026-11-12-the-plan.graph.json"
)

// publishCheckout is a real git checkout in a temporary directory: `init`, an
// origin the provenance reads, the files the test wrote, and one commit. It
// leaves the process standing in it, which is where the command reads the
// checkout from, and answers the commit it made.
//
// Everything git would otherwise read off the machine is replaced: a global or
// system config could set an includeIf, a hook or an identity, and a test that
// passed only because of one of those would not be a test of this command. The
// identity is a literal rather than a secret — a commit has to have one and
// nothing here checks its shape.
func publishCheckout(t *testing.T, files map[string]string) (dir, commit string) {
	t.Helper()
	for _, kv := range [][2]string{
		{"GIT_CONFIG_GLOBAL", os.DevNull},
		{"GIT_CONFIG_SYSTEM", os.DevNull},
		{"GIT_TERMINAL_PROMPT", "0"},
		{"GIT_AUTHOR_NAME", "A Person"},
		{"GIT_AUTHOR_EMAIL", "person@example.invalid"},
		{"GIT_COMMITTER_NAME", "A Person"},
		{"GIT_COMMITTER_EMAIL", "person@example.invalid"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	dir = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		var out bytes.Buffer
		c.Stdout, c.Stderr = &out, &out
		if err := c.Run(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out.String())
		}
		return strings.TrimSpace(out.String())
	}
	git("init", "-q", "-b", "main")
	git("remote", "add", "origin", "https://github.com/acme/app.git")
	writePlanFiles(t, dir, files)
	git("add", "-A")
	git("commit", "-q", "-m", "the plan")
	t.Chdir(dir)
	return dir, git("rev-parse", "HEAD")
}

// writePlanFiles writes a checkout's files, creating the directories they sit
// in. A key naming a symlink is written as one — "link:" and then its target —
// because that is the one shape a test of the confinement has to be able to
// make.
func writePlanFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if target, ok := strings.CutPrefix(body, "link:"); ok {
			if err := os.Symlink(target, p); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// publishDoor is the state every test below starts in: a signed-in person, a
// fake front door, and the CLI's seams pointed at them.
func publishDoor(t *testing.T) *fakeFrontDoor {
	t.Helper()
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")
	return door
}

// publishSent is the plan document one call carried, decoded into the record's
// own type: the CLI cannot import that type at run time (it holds the shape as
// a copy on purpose), so this is where the copy is held to it. A field renamed
// on one side and not the other decodes to its zero value here rather than
// reaching the record, and the assertions below fail.
func publishSent(t *testing.T, call fakeToolCall) coordinator.Plan {
	t.Helper()
	if call.Name != "roadmap_publish_plan" {
		t.Fatalf("the call was %q; want roadmap_publish_plan", call.Name)
	}
	raw, err := json.Marshal(call.Arguments["plan"])
	if err != nil {
		t.Fatal(err)
	}
	var doc coordinator.Plan
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("what the CLI sent does not decode as the record's plan document: %v", err)
	}
	return doc
}

// publishTools are the tool names the door served, in order, so that a test can
// assert the shape of a whole run rather than one call of it.
func publishTools(calls []fakeToolCall) []string {
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, c.Name)
	}
	return names
}

// TestPublishSendsTheTrimmedArtifactAndItsProvenance is the whole of a publish:
// the graph read out of the checkout, trimmed to the record's shape, carrying
// where it came from, and nothing else out of the tree.
func TestPublishSendsTheTrimmedArtifactAndItsProvenance(t *testing.T) {
	door := publishDoor(t)
	dir, commit := publishCheckout(t, map[string]string{planDoc: "# the plan\n", planArtifact: publishGraph})
	_ = dir

	out, _, err := execute(t, "coordinator", "roadmap", "publish", planDoc, "--json")
	if err != nil {
		t.Fatalf("roadmap publish: %v\n%s", err, out)
	}
	calls := door.toolCalls()
	if len(calls) != 2 {
		t.Fatalf("the door served %v; want the publish and one outcome read", publishTools(calls))
	}
	if calls[1].Name != "roadmap_request" {
		t.Errorf("the second call was %q; want the outcome read", calls[1].Name)
	}
	// The tool's schema is closed, so an argument the schema does not name is a
	// call the door would refuse: the document and its version, and no third
	// field.
	if len(calls[0].Arguments) != 2 {
		t.Errorf("the publish sent %v; the tool's schema is closed and holds plan and expect", calls[0].Arguments)
	}
	if v, _ := calls[0].Arguments["expect"].(string); v != "" {
		t.Errorf("the publish sent expect=%q; want the empty string for a plan the record does not hold", v)
	}

	doc := publishSent(t, calls[0])
	if doc.Plan != "plans/2026-11-12-the-plan" {
		t.Errorf("the plan id was %q; want the artifact's own place under docs/", doc.Plan)
	}
	if doc.Stream != "cli" {
		t.Errorf("the stream was %q; want the artifact's own", doc.Stream)
	}
	if doc.Source.Repo != "acme/app" {
		t.Errorf("the provenance named the repository %q; want the checkout's origin", doc.Source.Repo)
	}
	if doc.Source.Path != planArtifact {
		t.Errorf("the provenance named the path %q; want %q", doc.Source.Path, planArtifact)
	}
	if doc.Source.Commit != commit {
		t.Errorf("the provenance named the commit %q; want the one HEAD names, %q", doc.Source.Commit, commit)
	}
	if doc.Source.GraphSchema != 3 {
		t.Errorf("the provenance named graph_schema %d; want the artifact's own version", doc.Source.GraphSchema)
	}
	// The shape version is the door's to set (it writes RoadmapSchema over
	// whatever a caller sent), so a CLI that set one would be a second place
	// the record's version is decided.
	if doc.Schema != 0 {
		t.Errorf("the CLI sent schema=%d; the record's own version is the door's to set", doc.Schema)
	}

	if len(doc.Tasks) != 2 {
		t.Fatalf("the document carried %d tasks; want the two of the graph", len(doc.Tasks))
	}
	base, dep := doc.Tasks[0], doc.Tasks[1]
	if base.ID != "1" || base.Title != "the base" || base.Kind != "contract" {
		t.Errorf("the first task came across as %+v; want id, title and kind as the artifact wrote them", base)
	}
	if base.Size == nil || *base.Size != 3 {
		t.Errorf("the first task's size came across as %v; a schema-3 graph's size is what a before/after path is compared with", base.Size)
	}
	if base.Version == nil || *base.Version != "0.24" {
		t.Errorf("the first task's version came across as %v; want the artifact's own target", base.Version)
	}
	if dep.ID != "2" || dep.Kind != "" || dep.Size != nil || dep.Version != nil {
		t.Errorf("the second task came across as %+v; want an ordinary task with no size and no target version", dep)
	}
	if len(doc.Edges) != 1 || doc.Edges[0].Task != "2" || doc.Edges[0].Needs != "1" {
		t.Errorf("the edges came across as %+v; want the plan's own edge as two task ids", doc.Edges)
	}

	// **Nothing but the artifact leaves the checkout.** The record holds no
	// footprint, no run, no stub list, no decoupling record and no reason, and
	// each of those is a shape of the person's working tree — what a run
	// touched, what a contract leaves behind, why an edge exists — that this
	// command has no business carrying to a project-wide record.
	raw, err := json.Marshal(calls[0].Arguments["plan"])
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"footprint", "stubs", "decoupling", "contract_changes", "internal/base", "the-run-1", "the dependent is built against the base"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("the document sent carries %q; the trim is what keeps the checkout's own text in the checkout", leaked)
		}
	}
	// And nothing here reads the provenance back: this run derived it from the
	// checkout and sent it, and the human line names the plan and the outcome
	// rather than the commit — the `--json` document carries it because the
	// console asked for the field, not because anything consults it.
	plain, _, err := execute(t, "coordinator", "roadmap", "publish", planDoc)
	if err != nil {
		t.Fatalf("roadmap publish: %v\n%s", err, plain)
	}
	if !strings.Contains(plain, "plans/2026-11-12-the-plan") || !strings.Contains(plain, mcpauth.RoadmapApplied) {
		t.Errorf("the command printed %q; want the plan and what the record did with it", plain)
	}
	if strings.Contains(plain, commit) {
		t.Errorf("the command printed %q; the provenance is recorded and is not read back", commit)
	}
}

// TestPublishAllSendsEveryArtifact is the sweep: one call per artifact under
// docs/, in path order, each with its own provenance, and a file that is not a
// graph left where it is.
//
// It is the shape a person reaches for after adding a plan, and it is the one
// path the test list above does not name — the confinement test drives `--all`
// only as far as its refusal, and a sweep that read nothing would pass that.
func TestPublishAllSendsEveryArtifact(t *testing.T) {
	door := publishDoor(t)
	publishCheckout(t, map[string]string{
		"docs/notes.txt":                     "not a graph\n",
		"docs/plans/2026-11-12-a.md":         "# a\n",
		"docs/plans/2026-11-12-a.graph.json": publishGraph,
		"docs/setup-wizard/b.md":             "# b\n",
		"docs/setup-wizard/b.graph.json": strings.Replace(publishGraph,
			`"plan": "docs/plans/2026-11-12-the-plan.md"`, `"plan": "docs/setup-wizard/b.md"`, 1),
	})

	out, _, err := execute(t, "coordinator", "roadmap", "publish", "--all", "--json")
	if err != nil {
		t.Fatalf("roadmap publish --all: %v\n%s", err, out)
	}
	calls := door.toolCalls()
	if len(calls) != 4 {
		t.Fatalf("the door served %v; want two publishes and their two outcome reads", publishTools(calls))
	}
	// The ids are the artifacts' own places under docs/, by the rule
	// internal/plan holds for both readers: docs/plans/ is the one directory
	// whose files are named by their stem.
	for i, want := range []string{"plans/2026-11-12-a", "setup-wizard"} {
		doc := publishSent(t, calls[i*2])
		if doc.Plan != want {
			t.Errorf("call %d published %q; want %q, the artifacts in path order", i+1, doc.Plan, want)
		}
		if v, _ := calls[i*2].Arguments["expect"].(string); v != "" {
			t.Errorf("the sweep sent expect=%q; --all is refused with --expect, because one version cannot stand for many documents", v)
		}
	}
	var sent coordinatorPublishDoc
	if err := json.Unmarshal([]byte(out), &sent); err != nil {
		t.Fatalf("the --json document: %v\n%s", err, out)
	}
	if len(sent.Plans) != 2 {
		t.Fatalf("the document reported %d plans; want the two artifacts", len(sent.Plans))
	}
	// docs/notes.txt is not a graph and is not sent: the suffix is the rule,
	// and a sweep that read every file under docs/ would publish the plan
	// documents themselves.
	for _, c := range calls {
		if strings.Contains(string(mustJSON(t, c.Arguments)), "notes.txt") {
			t.Error("the sweep read a file that is not a graph artifact")
		}
	}
}

// mustJSON marshals a call's arguments for a test that is looking for a string
// in what was sent.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestPublishTwiceWritesOnce is the red step, and it is the property the whole
// idempotence rests on: the two runs send the same bytes.
//
// P9b's writer compares before it puts, so the second publish of one artifact
// writes nothing — but only if the second document is the first one. A publish
// that stamped anything of its own into the document (a time, a counter, the
// person's name) would send a different document every run, the record would
// see a change every time, and every publish would be a write. There is nothing
// in this command that could answer "when was this published" without that, and
// nothing asks it to: the record's own `at` is the apply job's.
func TestPublishTwiceWritesOnce(t *testing.T) {
	door := publishDoor(t)
	publishCheckout(t, map[string]string{planDoc: "# the plan\n", planArtifact: publishGraph})

	for i := range 2 {
		if _, _, err := execute(t, "coordinator", "roadmap", "publish", planDoc); err != nil {
			t.Fatalf("publish %d: %v", i+1, err)
		}
	}
	calls := door.toolCalls()
	if len(calls) != 4 {
		t.Fatalf("the door served %v; want two publishes and their two outcome reads", publishTools(calls))
	}
	first, err := json.Marshal(calls[0].Arguments)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(calls[2].Arguments)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("the two publishes sent different documents:\n%s\n%s\nthe record compares before it puts, so a document that differs every run is a write every run", first, second)
	}
}

// TestPublishWithAStaleVersionIsRefused: `--expect` is the version the person
// last read, and the record compares it with what it holds. Two people
// publishing one plan from two checkouts cannot silently overwrite each other,
// and the refusal names the version to read again.
func TestPublishWithAStaleVersionIsRefused(t *testing.T) {
	door := publishDoor(t)
	publishCheckout(t, map[string]string{planDoc: "# the plan\n", planArtifact: publishGraph})
	door.serve(mcpauth.RoadmapRefused,
		"the record has moved since this request was built: it holds the version v3 and the request expected v2; read the record again and send what it holds", "v3")

	out, _, err := execute(t, "coordinator", "roadmap", "publish", planDoc, "--expect", "v2")
	if err == nil {
		t.Fatalf("a publish the record refused returned no error:\n%s", out)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("exit %d, want a user error: the record refused it, and reading again is the person's to do", fcli.ExitCode(err))
	}
	calls := door.toolCalls()
	if len(calls) == 0 {
		t.Fatal("the door was not called")
	}
	if v, _ := calls[0].Arguments["expect"].(string); v != "v2" {
		t.Errorf("the publish sent expect=%q; want the version the person read", v)
	}
	for _, want := range []string{"the record has moved", "v2", "v3"} {
		if !strings.Contains(out, want) {
			t.Errorf("the command printed %q; want %q in it", out, want)
		}
	}
}

// TestPublishRefusesAnUncommittedArtifact: what is published is what is
// committed (D30's "clobber uncommitted work", turned around). A graph edited
// and not committed is a graph the commit the document names does not hold, so
// the provenance would be a lie; the person is told that instead.
func TestPublishRefusesAnUncommittedArtifact(t *testing.T) {
	door := publishDoor(t)
	dir, _ := publishCheckout(t, map[string]string{planDoc: "# the plan\n", planArtifact: publishGraph})
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(planArtifact)),
		[]byte(strings.Replace(publishGraph, "the base", "the base, edited", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := execute(t, "coordinator", "roadmap", "publish", planDoc)
	if err == nil {
		t.Fatalf("a publish of an uncommitted artifact returned no error:\n%s", out)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("exit %d, want a user error", fcli.ExitCode(err))
	}
	if !strings.Contains(err.Error(), "committed") {
		t.Errorf("the command said %q; want it to say the artifact has changes that are not committed", err)
	}
	// The checkout is read before the front door is opened: a refusal a person
	// can fix without the installation costs no token exchange and queues
	// nothing.
	if calls := door.toolCalls(); len(calls) != 0 {
		t.Errorf("the door was called with %v; an uncommitted artifact is refused before anything is sent", publishTools(calls))
	}
}

// TestPublishRefusesARefusedArtifactWhole: an artifact internal/plan refuses is
// refused whole. Half a plan is not a plan, and a document that reached the
// record with a task missing is one nobody would notice until the graph was
// drawn.
func TestPublishRefusesARefusedArtifactWhole(t *testing.T) {
	door := publishDoor(t)
	publishCheckout(t, map[string]string{
		planDoc: "# the plan\n",
		// No schema_version at all: the refusal internal/plan makes first, and
		// the one that says which build reads the file.
		planArtifact: `{"plan": "docs/plans/2026-11-12-the-plan.md", "nodes": [{"id": "1", "title": "the base", "run": null, "version": null, "footprint": {"prediction": true, "paths": []}}], "edges": []}`,
	})

	out, _, err := execute(t, "coordinator", "roadmap", "publish", planDoc)
	if err == nil {
		t.Fatalf("a publish of a refused artifact returned no error:\n%s", out)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("exit %d, want a user error", fcli.ExitCode(err))
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("the command said %q; want the graph reader's own refusal, which names what is missing", err)
	}
	if calls := door.toolCalls(); len(calls) != 0 {
		t.Errorf("the door was called with %v; a refused artifact is refused whole, before anything is sent", publishTools(calls))
	}
}

// TestPublishStaysInTheCheckout: GV1.6's confined walk, whose whole point is
// that the text which leaves for a project-wide record is text the checkout
// holds.
//
// A symlink is the way it could stop being that — a link out of the tree makes
// the file the command reads, and publishes, something the repository never
// contained, and the person publishing it sees a path in their own checkout.
// So a link is refused rather than read through, whoever made it and wherever
// it points.
func TestPublishStaysInTheCheckout(t *testing.T) {
	t.Run("a symlinked artifact", func(t *testing.T) {
		door := publishDoor(t)
		outside := filepath.Join(t.TempDir(), "elsewhere.graph.json")
		if err := os.WriteFile(outside, []byte(publishGraph), 0o644); err != nil {
			t.Fatal(err)
		}
		publishCheckout(t, map[string]string{planDoc: "# the plan\n", planArtifact: "link:" + outside})

		out, _, err := execute(t, "coordinator", "roadmap", "publish", planDoc)
		if err == nil {
			t.Fatalf("a publish through a symlink returned no error:\n%s", out)
		}
		if fcli.ExitCode(err) != fcli.ExitUserError {
			t.Errorf("exit %d, want a user error", fcli.ExitCode(err))
		}
		if !strings.Contains(err.Error(), "symlink") {
			t.Errorf("the command said %q; want it to name the link it refused to follow", err)
		}
		if calls := door.toolCalls(); len(calls) != 0 {
			t.Errorf("the door was called with %v; nothing outside the checkout may be published", publishTools(calls))
		}
	})

	t.Run("a symlinked docs", func(t *testing.T) {
		door := publishDoor(t)
		// The whole directory is a link, which is the case the per-file check
		// above cannot see: --all walks docs/, and a walk that followed this
		// would read every graph of a directory outside the checkout.
		elsewhere := t.TempDir()
		writePlanFiles(t, elsewhere, map[string]string{"plans/2026-11-12-the-plan.graph.json": publishGraph})
		dir := t.TempDir()
		if err := os.Symlink(elsewhere, filepath.Join(dir, "docs")); err != nil {
			t.Fatal(err)
		}
		t.Chdir(dir)

		out, _, err := execute(t, "coordinator", "roadmap", "publish", "--all")
		if err == nil {
			t.Fatalf("a sweep through a symlinked docs/ returned no error:\n%s", out)
		}
		if fcli.ExitCode(err) != fcli.ExitUserError {
			t.Errorf("exit %d, want a user error", fcli.ExitCode(err))
		}
		if !strings.Contains(err.Error(), "symlink") {
			t.Errorf("the command said %q; want it to name the link it refused to follow", err)
		}
		if calls := door.toolCalls(); len(calls) != 0 {
			t.Errorf("the door was called with %v; a directory outside the checkout is not this checkout's plans", publishTools(calls))
		}
	})
}

// TestPublishFromAnotherRepositoryIsRefusedWithoutReplaceSource is D2123: a
// plan id is a project-wide name, so two repositories can publish a plan of the
// same id, and a publish from one silently replacing the other's is the one
// place the provenance is worth a sentence.
//
// **The refusal comes from the record, and the flag that answers it does not
// exist yet.** The tool's argument schema and mcpauth's request record carry no
// `replace_source` (P9d was built without it), so this command has nothing to
// send: `--replace-source` here would be a flag that silently did nothing,
// which is worse than the gap. The command surfaces the record's sentence,
// which is the whole of what it can honestly do until the wire carries the
// field — reported rather than guessed at.
func TestPublishFromAnotherRepositoryIsRefusedWithoutReplaceSource(t *testing.T) {
	door := publishDoor(t)
	publishCheckout(t, map[string]string{planDoc: "# the plan\n", planArtifact: publishGraph})
	door.refuseWrite("the plan plans/2026-11-12-the-plan is already published from acme/other, and this publish comes from acme/app; pass --replace-source to publish over another repository's plan")

	out, _, err := execute(t, "coordinator", "roadmap", "publish", planDoc)
	if err == nil {
		t.Fatalf("a publish of another repository's plan returned no error:\n%s", out)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("exit %d, want a user error: the record refused it", fcli.ExitCode(err))
	}
	for _, want := range []string{"acme/other", "acme/app", "replace-source"} {
		if !strings.Contains(out, want) {
			t.Errorf("the command printed %q; want the record's refusal, which names %q", out, want)
		}
	}
	// And the flag the refusal tells the person to pass is not one this build
	// has: cobra refuses it, which is what makes the gap visible rather than
	// silent.
	if _, _, err := execute(t, "coordinator", "roadmap", "publish", planDoc, "--replace-source"); err == nil {
		t.Error("--replace-source was accepted; the wire carries no such field, so a flag that takes it would do nothing")
	}
}

// TestPublishReleasesFlagSkipsWhatExists is D2120's catalogue import: one
// release per stream, and — the half that matters for a file somebody runs
// twice — a release the record already holds is skipped rather than failed.
//
// The record answers both with a refusal before it queues anything, so the
// distinction is made here: a refusal of an import is the ordinary state of a
// second import, and a command that exited non-zero on it would fail the first
// run of every release that was already in the record.
func TestPublishReleasesFlagSkipsWhatExists(t *testing.T) {
	door := publishDoor(t)
	publishCheckout(t, map[string]string{"docs/release-streams.yaml": publishCatalogue})

	out, _, err := execute(t, "coordinator", "roadmap", "publish", "--releases", "docs/release-streams.yaml", "--json")
	if err != nil {
		t.Fatalf("the first import: %v\n%s", err, out)
	}
	calls := door.toolCalls()
	if len(calls) != 6 {
		t.Fatalf("the door served %v; want three releases and their three outcome reads", publishTools(calls))
	}
	// One release per stream, named by the catalogue: the version it is at, the
	// name that version spent, the state the catalogue means, and the position
	// in the stream the version says.
	for _, want := range []struct {
		id, stream, name string
		order            float64
	}{
		{"0.11.0", "cli", "Badger", 1},
		{"0.111.0", "console", "Mango", 1},
		{"0.201.0", "mobile", "Pelican", 1},
	} {
		var found bool
		for _, c := range calls {
			if c.Name != "roadmap_release_set" {
				continue
			}
			if got, _ := c.Arguments["id"].(string); got != want.id {
				continue
			}
			found = true
			for key, value := range map[string]string{
				"stream": want.stream, "name": want.name, "state": "finished", "expect": "",
			} {
				if got, _ := c.Arguments[key].(string); got != value {
					t.Errorf("release %s sent %s=%q; want %q", want.id, key, got, value)
				}
			}
			if got, _ := c.Arguments["order"].(float64); got != want.order {
				t.Errorf("release %s sent order=%v; want %v", want.id, c.Arguments["order"], want.order)
			}
		}
		if !found {
			t.Errorf("no release was sent for %s %s", want.stream, want.id)
		}
	}
	var first coordinatorPublishDoc
	if err := json.Unmarshal([]byte(out), &first); err != nil {
		t.Fatalf("the --json document: %v\n%s", err, out)
	}
	for _, r := range first.Releases {
		if r.Status != mcpauth.RoadmapApplied {
			t.Errorf("the first import reported %+v; want every release applied", r)
		}
	}

	// The same catalogue again, against a record that holds it.
	door.refuseWrite("the roadmap record already holds this release, and a release is created with the empty version and changed only from the version you read")
	out2, _, err := execute(t, "coordinator", "roadmap", "publish", "--releases", "docs/release-streams.yaml", "--json")
	if err != nil {
		t.Fatalf("the second import: %v\n%s", err, out2)
	}
	var second coordinatorPublishDoc
	if err := json.Unmarshal([]byte(out2), &second); err != nil {
		t.Fatalf("the --json document: %v\n%s", err, out2)
	}
	if len(second.Releases) != 3 {
		t.Fatalf("the second import reported %d releases; want the three of the catalogue", len(second.Releases))
	}
	for _, r := range second.Releases {
		if r.Status != coordinatorPublishSkipped {
			t.Errorf("the second import reported %+v; want a release the record holds skipped rather than failed", r)
		}
		if r.Reason == "" {
			t.Errorf("the skip of %s carries no reason; the record's own sentence is what says why", r.Release)
		}
	}
}

// TestPublishWithoutACatalogueNeedsNone is D2113's second half: the catalogue is
// a flag a person passes and never a default. A repository that carries no
// `docs/release-streams.yaml` — which is every repository but this one — is not
// a repository with an incomplete installation, and its plans publish exactly
// the same way.
func TestPublishWithoutACatalogueNeedsNone(t *testing.T) {
	door := publishDoor(t)
	publishCheckout(t, map[string]string{planDoc: "# the plan\n", planArtifact: publishGraph})

	out, _, err := execute(t, "coordinator", "roadmap", "publish", planDoc)
	if err != nil {
		t.Fatalf("a publish in a checkout with no catalogue: %v\n%s", err, out)
	}
	for _, c := range door.toolCalls() {
		if c.Name == "roadmap_release_set" {
			t.Errorf("the publish called %s; a catalogue is never looked for and never a default", c.Name)
		}
	}
	// And naming one that is not there is the catalogue reader's refusal rather
	// than a silent skip, because a person who passed the flag meant it.
	if _, _, err := execute(t, "coordinator", "roadmap", "publish", "--releases", "docs/release-streams.yaml"); err == nil {
		t.Error("--releases naming a file that is not there was accepted")
	} else if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("exit %d; want a user error", fcli.ExitCode(err))
	}
}

// TestPublishNeverWritesTheDatabaseItself is the property the whole route
// exists for (D2118, D922/H3), held over the one file of the group that reads
// a person's working tree: what it does with that text is call the front
// door's tool.
//
// **This is the mutation's assertion.** A publish that reached P9b's writer
// with the person's credential — the shape of the mistake this design guards
// against — has to import the record's package or a database client to do it,
// and the first of those two checks refuses it. The second catches the same
// mistake made through a helper this package already has.
func TestPublishNeverWritesTheDatabaseItself(t *testing.T) {
	const name = "roadmap_publish.go"
	forbidden := []string{
		"github.com/dimipaun/fugaro/ee/coordinator",
		"github.com/dimipaun/fugaro/internal/rtdb",
		"github.com/dimipaun/fugaro/internal/runstore",
		"github.com/dimipaun/fugaro/internal/budget",
		"github.com/dimipaun/fugaro/internal/blobx",
	}
	writers := []string{"openCoordinatorStore", "CoordinatorDB", "coordinator.Store", "rtdb.", "runstore."}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		for _, bad := range forbidden {
			if path == bad {
				t.Errorf("%s imports %s; the publish reaches the record through the front door's tool and holds no writer of its own", name, bad)
			}
		}
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range writers {
		if strings.Contains(string(data), bad) {
			t.Errorf("%s mentions %s; a publish that can write the record has been routed around the front door", name, bad)
		}
	}
	// And the positive half: the writes it does make are the door's own tool
	// names, so a rename on either side shows up here rather than as a -32601
	// nobody sees until a person publishes.
	for _, want := range []string{"roadmap_publish_plan", "roadmap_unpublish_plan", "roadmap_release_set"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not name %s, so this test is asserting nothing about the route it is meant to pin", name, want)
		}
	}
}
