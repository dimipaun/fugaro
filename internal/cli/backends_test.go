package cli

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// fugaro backends ls (plan 2026-10-19-compute-backend.md Task 8 step 3, design
// docs/design/compute-backend.md §4.4, P7: build it). Two things are worth a
// test and they are different things: the command answers the design's question
// ("which backend does this project use?") from the project config, offline;
// and the names it lists are the seam's own, so the list can neither offer a
// backend nothing can open nor hide one that exists.

// TestBackendsLsMarksTheProjectsOwnBackend: the design's answer, both forms.
// A project whose config names no backend runs on cloud-run (the default), so
// cloud-run is the marked line and cloud-batch is listed and unmarked. The
// answer comes from localcfg.Config.Backend and from nothing else.
func TestBackendsLsMarksTheProjectsOwnBackend(t *testing.T) {
	f := newCloudFixture(t) // project aurora, no backend: key
	out, _, err := execute(t, "backends", "ls")
	if err != nil {
		t.Fatalf("backends ls: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("backends ls printed %d lines (%q), want one line per selectable backend", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], backend.CloudRun+"\t") || !strings.Contains(lines[0], "aurora") {
		t.Errorf("line 1 = %q, want %s marked as aurora's backend", lines[0], backend.CloudRun)
	}
	if lines[1] != backend.CloudBatch {
		t.Errorf("line 2 = %q, want %s unmarked", lines[1], backend.CloudBatch)
	}

	out, _, err = execute(t, "backends", "ls", "--json")
	if err != nil {
		t.Fatalf("backends ls --json: %v", err)
	}
	var doc backendsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if doc.Project != "aurora" || doc.Backend != backend.CloudRun {
		t.Errorf("doc = %+v, want project aurora on %s", doc, backend.CloudRun)
	}
	if !doc.Complete || doc.Note != "" {
		t.Errorf("doc = %+v, want complete=true with no note (selection succeeded)", doc)
	}
	want := []backendsEntry{{Name: backend.CloudRun, InUse: true}, {Name: backend.CloudBatch}}
	if !reflect.DeepEqual(doc.Backends, want) {
		t.Errorf("backends = %+v, want %+v", doc.Backends, want)
	}

	// The whole point of P7's recommendation: the question is answered without
	// a cloud round trip. Nothing reached the fakes.
	if n := len(f.run.Requests()); n != 0 {
		t.Errorf("backends ls made %d call(s) to the Cloud Run API, want none: it is a local read of the project config", n)
	}
	if n := len(f.logging.Requests()); n != 0 {
		t.Errorf("backends ls made %d call(s) to Cloud Logging, want none", n)
	}
}

// TestBackendsLsNoProjectConfigNeedsNoCloud: with nothing selected at all the
// command still lists both names -- that half of its answer is a fact about
// this build, not about the project -- and says so rather than inventing a
// project's backend. complete=false is what tells a --json caller the
// difference (the same field accountsListDoc carries, for the same reason).
func TestBackendsLsNoProjectConfigNeedsNoCloud(t *testing.T) {
	isolateProjects(t, t.TempDir())
	out, errOut, err := execute(t, "backends", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc backendsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if doc.Complete || doc.Note == "" {
		t.Fatalf("doc = %+v, want complete=false with a note (nothing could be selected)", doc)
	}
	if doc.Project != "" || doc.Backend != "" {
		t.Errorf("doc = %+v, want no project and no backend named", doc)
	}
	if len(doc.Backends) != 2 || doc.Backends[0].InUse || doc.Backends[1].InUse {
		t.Errorf("backends = %+v, want both names, neither marked in use", doc.Backends)
	}
	if !strings.Contains(errOut, "note: ") {
		t.Errorf("stderr = %q, want the note that nothing was selected", errOut)
	}
}

// TestBackendsLsListsTheOpenerMapsNames is the guard the command's list needs:
// every name it prints is a name backend.Open can open, and every name the seam
// can open is printed. selectableBackends is a list of the seam's constants and
// the opener map is the registry (Global Constraint 2: no second way to select
// a backend, so the list may not drift from the one that selects).
//
// backend.Local is the case this is really for: it is a backend name this build
// knows and it is not a project's to select, so a list that picked it up (or an
// opener map that grew one) fails here rather than in a user's run.
func TestBackendsLsListsTheOpenerMapsNames(t *testing.T) {
	ctx := context.Background()
	run := gcpfake.NewRun(t)
	run.Project, run.Region = "proj-1234", "us-east5"
	// The fixture is a fully provisioned installation (the same one
	// cloud_openers_test.go's rig builds), because the point here is that the
	// map opens every name the command prints: half an installation would fail
	// on the Batch side for a reason that has nothing to do with the list.
	rig := newBatchRig(t, 412)
	openers := rig.openers(gcp.Options{GCPProject: rig.lc.GCPProject, Region: rig.lc.Region,
		Endpoints: gcp.Endpoints{Run: run.URL + "/", NoAuth: true}})

	if len(openers) != len(selectableBackends) {
		t.Fatalf("the opener map has %d entries and backends ls lists %d: a backend one of them knows and the other does not is a name fugaro offers and then refuses", len(openers), len(selectableBackends))
	}
	for _, name := range selectableBackends {
		be, err := backend.Open(ctx, name, openers)
		if err != nil {
			t.Errorf("backends ls lists %q, which the seam does not open: %v", name, err)
			continue
		}
		if got := be.Codec().Backend(); got != name {
			t.Errorf("opening %q gave a backend whose codec says %q: the printed name is not the name the run would record", name, got)
		}
	}
	if _, ok := openers[backend.Local]; ok {
		t.Errorf("the opener map carries %s, which is an invocation's own platform and never a project's backend", backend.Local)
	}
	// A name printed twice would be a table a reader cannot count.
	sorted := append([]string(nil), selectableBackends...)
	sort.Strings(sorted)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			t.Errorf("backends ls lists %q twice", sorted[i])
		}
	}
	// The list is the seam's two selectable names, and this is the assertion
	// that fails if a third is added to one side and not the other.
	want := []string{backend.CloudBatch, backend.CloudRun}
	if !reflect.DeepEqual(sorted, want) {
		t.Errorf("selectableBackends = %v, want the seam's %v", selectableBackends, want)
	}
}

// TestBackendsLsSpellsNoNameAsAString: the names are the seam's constants, not
// literals typed into this file (Global Constraint 2). A string literal that
// agreed with a constant today is a name that stops matching the day the seam
// renames one, and both the table and the opener map are read from the
// constants, so the mismatch would be silent.
//
// It is a source check because the drift it catches is a rename on the seam's
// side, which no test that calls the constants could see.
func TestBackendsLsSpellsNoNameAsAString(t *testing.T) {
	data, err := os.ReadFile("backends.go")
	if err != nil {
		t.Fatal(err)
	}
	code := stripLineComments(string(data))
	for _, bad := range []string{`"cloud-run"`, `"cloud-batch"`, `"local"`} {
		if strings.Contains(code, bad) {
			t.Errorf("backends.go spells a backend name as the literal %s: the names come from internal/backend's constants", bad)
		}
	}
	if want := "var selectableBackends = []string{backend.CloudRun, backend.CloudBatch}"; !strings.Contains(code, want) {
		t.Errorf("backends.go no longer contains %q: the list is the seam's constants", want)
	}
	// The marked line is the project's own answer, never a constant: a command
	// that marked cloud-run unconditionally would be right for every project
	// this build can select today and wrong the moment one can select another.
	if want := "project, inUse = lc.Name, lc.Backend()"; !strings.Contains(code, want) {
		t.Errorf("backends.go no longer contains %q: which backend is in use is the project config's answer", want)
	}
	for _, bad := range []string{"inUse = backend.CloudRun", "inUse = backend.CloudBatch", "inUse = backend.Local"} {
		if strings.Contains(code, bad) {
			t.Errorf("backends.go marks a backend with %q: the marked name comes from the project's own config", bad)
		}
	}
}

// TestBackendsUnknownSubcommand: the noun's own error, the shape accounts and
// recipes use, and a bare `fugaro backends` that helps rather than failing.
func TestBackendsUnknownSubcommand(t *testing.T) {
	out, _, err := execute(t, "backends")
	if err != nil {
		t.Fatalf("bare backends: %v, want help", err)
	}
	if !strings.Contains(out, "ls") {
		t.Errorf("bare backends printed %q, want the help naming its ls subcommand", out)
	}
	_, _, err = execute(t, "backends", "list")
	if err == nil || !strings.Contains(err.Error(), "unknown backends subcommand") {
		t.Fatalf("backends list: err = %v, want the unknown-subcommand refusal", err)
	}
}

// TestBackendsLsHelpNamesTheRefusedBackend: the command lists a name this build
// refuses to select, so its help has to say which one that is and why,
// otherwise the second line of its own output reads as an offer. Both claims
// are checked against localcfg's real behaviour in the same test, so the help
// and the refusal cannot drift apart.
func TestBackendsLsHelpNamesTheRefusedBackend(t *testing.T) {
	cfg := func(backendName string) []byte {
		return []byte("version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: my-runs\n" +
			"backend: " + backendName + "\nrepos:\n  acme/app: { provider: github, workflows: [web] }\n")
	}
	if _, err := localcfg.Parse(cfg(backend.CloudRun)); err != nil {
		t.Fatalf("a config naming %s is refused, and it is the backend this build runs: %v", backend.CloudRun, err)
	}
	_, err := localcfg.Parse(cfg(backend.CloudBatch))
	if err == nil || !strings.Contains(err.Error(), "not enabled yet") {
		t.Fatalf("a config naming %s: err = %v, want the not-enabled-yet refusal the help describes", backend.CloudBatch, err)
	}
	out, _, err := execute(t, "backends", "ls", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{backend.CloudBatch, "not enabled yet", backend.CloudRun} {
		if !strings.Contains(out, want) {
			t.Errorf("backends ls --help does not mention %q, and it lists a backend this build refuses to select", want)
		}
	}
}
