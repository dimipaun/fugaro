package apply

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// pendingHome is a machine's config home as the reader and the writer see it:
// XDG_CONFIG_HOME under a temporary directory, which is the only home these
// tests touch. The project config itself is never written — the pending
// document sits beside it and nothing here reads the config.
func pendingHome(t *testing.T) func(string) string {
	t.Helper()
	home := t.TempDir()
	return func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return home
		}
		return ""
	}
}

// pendingProjectsDir is where the documents of pendingHome's machine live.
func pendingProjectsDir(t *testing.T, getenv func(string) string) string {
	t.Helper()
	dir, err := PendingPath(getenv, "workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(dir)
}

// TestPendingNeverHoldsAValue: the document names the change that asked for a
// step and never what was set (D1570). It is the one rule that keeps the file
// from becoming a second copy of the config — a place a loosened ceiling, or a
// secret a command wrote, would be written down a second time, on a machine
// whose config is not the authority and whose reader is not the writer.
//
// Both halves are pinned. The shape: the members of the document, of an entry
// and of the change are exactly the ones schemas/pending-steps.schema.json
// declares, and none of them is a member a value could travel in. The
// behaviour: a document built the way a command builds it, for a change whose
// value is a distinctive string, is written without that string anywhere in it.
func TestPendingNeverHoldsAValue(t *testing.T) {
	// The value a command would have written into the config, and the only
	// thing that must never reach this file.
	const setValue = "pending-store-must-not-hold-this"

	want := []string{"change", "gcp_project", "project", "step"}
	if got := jsonMembers(t, PendingStep{}); !slices.Equal(got, want) {
		t.Errorf("an entry's members are %v, want %v", got, want)
	}
	if got, want := jsonMembers(t, Change{}), []string{"key", "kind"}; !slices.Equal(got, want) {
		t.Errorf("a change's members are %v, want %v", got, want)
	}
	if got, want := jsonMembers(t, Pending{}), []string{"last_apply", "steps", "version"}; !slices.Equal(got, want) {
		t.Errorf("the document's members are %v, want %v", got, want)
	}

	// A member a value could travel in, under any name it is likely to take.
	for _, members := range [][]string{jsonMembers(t, Pending{}), jsonMembers(t, PendingStep{}), jsonMembers(t, Change{})} {
		for _, name := range members {
			switch name {
			case "value", "new", "old", "secret", "content", "setting", "text":
				t.Errorf("%q is a member a value could be written in, and the pending document holds no value (D1570)", name)
			}
		}
	}

	// The schema is the contract the shape is read by, so it declares the same
	// members and closes each object: a document carrying one this build does
	// not know is refused, which is what makes the member list the whole of it.
	schema := readSchema(t)
	if got := schemaMembers(t, schema); !slices.Equal(got, []string{"last_apply", "steps", "version"}) {
		t.Errorf("the schema's document members are %v, want last_apply, steps and version", got)
	}
	if got := schemaMembers(t, schema, "properties", "steps", "items"); !slices.Equal(got, want) {
		t.Errorf("the schema's entry members are %v, want %v", got, want)
	}
	if got := schemaMembers(t, schema, "properties", "steps", "items", "properties", "change"); !slices.Equal(got, []string{"key", "kind"}) {
		t.Errorf("the schema's change members are %v, want key and kind", got)
	}
	for _, path := range [][]string{
		{},
		{"properties", "steps", "items"},
		{"properties", "steps", "items", "properties", "change"},
	} {
		if closed := schemaClosed(t, schema, path...); !closed {
			t.Errorf("the schema's object at %v is not closed: the reader decodes strictly, so a member it does not know is an error", path)
		}
	}

	// And the write itself: the entry a command queues for that change, saved,
	// holds the key and not the value.
	getenv := pendingHome(t)
	p := EmptyPending()
	p.Add(PendingStep{
		Step:       initflow.Policy,
		Project:    "workspace-a",
		GCPProject: "proj-1234",
		Change:     Change{Kind: Setting, Key: "image_catalog.entries.*.description"},
	})
	if err := SavePending(getenv, "workspace-a", p); err != nil {
		t.Fatal(err)
	}
	path, err := PendingPath(getenv, "workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(setValue)) {
		t.Errorf("the pending document holds %q, the value of the change: it may hold the change's subject and never its value (D1570)", setValue)
	}
	if !bytes.Contains(data, []byte("image_catalog.entries.*.description")) {
		t.Errorf("the pending document does not name the change that asked for the step:\n%s", data)
	}
}

// TestPendingRefusesAProjectMismatch: every entry is bound to the project name
// and the GCP project ID it was queued for, and a read refuses an entry bound
// elsewhere (D1575). A file that travelled — restored into another project's
// config directory, or copied to a machine whose installation is another GCP
// project — would otherwise run its steps against an installation the change
// was never made for.
func TestPendingRefusesAProjectMismatch(t *testing.T) {
	getenv := pendingHome(t)
	dir := pendingProjectsDir(t, getenv)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "workspace-a"+PendingFileSuffix)
	write := func(t *testing.T, project, gcpProject string) {
		t.Helper()
		doc := `{"version":1,"steps":[{"step":"policy","project":"` + project + `","gcp_project":"` + gcpProject + `",` +
			`"change":{"kind":"setting","key":"image_catalog.entries.*.name"}}]}`
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(t, "workspace-b", "proj-1234")
	if _, err := LoadPending(getenv, "workspace-a", "proj-1234"); !errors.Is(err, ErrProjectMismatch) {
		t.Errorf("a document bound to another project loaded with err = %v, want ErrProjectMismatch", err)
	}
	write(t, "workspace-a", "proj-9999")
	_, err := LoadPending(getenv, "workspace-a", "proj-1234")
	if !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("a document bound to another GCP project loaded with err = %v, want ErrProjectMismatch", err)
	}

	// The refusal names both sides, so the person reading it can see which
	// project queued the steps and which one they are acting on.
	for _, want := range []string{"workspace-a", "proj-9999", "proj-1234", initflow.Policy} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}

	write(t, "workspace-a", "proj-1234")
	got, err := LoadPending(getenv, "workspace-a", "proj-1234")
	if err != nil {
		t.Fatalf("the document bound to this project did not load: %v", err)
	}
	if len(got.Steps) != 1 || got.Steps[0].Step != initflow.Policy {
		t.Errorf("the document loaded as %+v, want the one policy step", got.Steps)
	}
}

// TestPendingFileVersioned: the document carries its own version, is refused
// when it is another one's, and is never written over a newer fugaro's. It also
// pins where the file lives — beside the project config, named so that
// localcfg.Projects can never read it as a project config — and the state
// rules the rest of the file follows: no file is an empty document and no
// error, a write is 0600 in a 0700 directory, and what was written round-trips
// member for member.
func TestPendingFileVersioned(t *testing.T) {
	getenv := pendingHome(t)
	path, err := PendingPath(getenv, "workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	configPath, err := localcfg.ProjectPath(getenv, "workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(filepath.Dir(configPath), "workspace-a"+PendingFileSuffix); path != want {
		t.Errorf("the pending document is %s, want %s: beside the project config", path, want)
	}
	// The name is what keeps a project config's own directory honest: the file
	// beside the config may not be read back as a second project.
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("name: workspace-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing was written and %s exists", path)
	}

	// A machine that has recorded nothing owes nothing, and that is not an
	// error: every reader starts here (D1575).
	empty, err := LoadPending(getenv, "workspace-a", "proj-1234")
	if err != nil {
		t.Fatalf("LoadPending with no file: %v", err)
	}
	if empty.Version != PendingVersion || len(empty.Steps) != 0 {
		t.Errorf("the empty document is %+v, want version %d and no steps", empty, PendingVersion)
	}

	p := EmptyPending()
	// The stamp is the caller's: a placeholder here, and nothing in this
	// package reads or compares it (D1575).
	p.LastApply = "sha256:0"
	p.Add(PendingStep{
		Step:       CatalogBuild,
		Project:    "workspace-a",
		GCPProject: "proj-1234",
		Change:     Change{Kind: Resource, Key: ResourceImage},
	})
	p.Add(PendingStep{
		Step:       initflow.Repository,
		Project:    "workspace-a",
		GCPProject: "proj-1234",
		Change:     Change{Kind: Resource, Key: ResourceImage},
	})
	if err := SavePending(getenv, "workspace-a", p); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPending(getenv, "workspace-a", "proj-1234")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != PendingVersion {
		t.Errorf("version = %d, want %d", got.Version, PendingVersion)
	}
	if got.LastApply != p.LastApply {
		t.Errorf("last_apply = %q, want %q", got.LastApply, p.LastApply)
	}
	if !slices.Equal(got.Steps, p.Steps) {
		t.Errorf("the entries came back as %+v, want %+v", got.Steps, p.Steps)
	}
	if mode, err := fileMode(path); err != nil || mode != 0o600 {
		t.Errorf("the document's mode is %v (err %v), want 0600", mode, err)
	}
	if mode, err := fileMode(filepath.Dir(path)); err != nil || mode != 0o700 {
		t.Errorf("the directory's mode is %v (err %v), want 0700", mode, err)
	}
	// And the file beside the config is not one: the projects directory lists
	// the project config alone.
	projects, err := localcfg.Projects(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(projects, []string{"workspace-a"}) {
		t.Errorf("localcfg.Projects lists %v, want the one project config: the pending document may not read back as a project", projects)
	}

	// A document from another build: refused by the reader, and never written
	// over by the writer — this build cannot read it, and rebuilding it from
	// nothing would drop that fugaro's record of what is owed.
	newer := `{"version":2,"steps":[]}`
	if err := os.WriteFile(path, []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPending(getenv, "workspace-a", "proj-1234"); err == nil {
		t.Error("a version 2 document loaded: the reader has to refuse a version it does not write")
	}
	if err := SavePending(getenv, "workspace-a", EmptyPending()); err == nil {
		t.Error("a version 2 document was written over: a newer fugaro's record of what is owed has to survive this one")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != newer {
		t.Errorf("the version 2 document is now %q (err %v), want it left alone", data, err)
	}

	// A document this build cannot parse is state gone wrong and is rebuilt:
	// the file is a hint, and the last_apply stamp is what says whether
	// anything was owed (D1575).
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SavePending(getenv, "workspace-a", EmptyPending()); err != nil {
		t.Errorf("a corrupt document refused the write: %v", err)
	}
	if _, err := LoadPending(getenv, "workspace-a", "proj-1234"); err != nil {
		t.Errorf("the rewritten document does not load: %v", err)
	}
}

// jsonMembers is the JSON member names of a document struct, sorted.
func jsonMembers(t *testing.T, v any) []string {
	t.Helper()
	typ := reflect.TypeOf(v)
	names := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name == "" {
			t.Fatalf("%s.%s has no JSON member name", typ.Name(), typ.Field(i).Name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// readSchema is schemas/pending-steps.schema.json as a document.
func readSchema(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "pending-steps.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// schemaMembers walks the schema down path and returns the member names the
// closed object at its end declares, sorted.
func schemaMembers(t *testing.T, doc map[string]any, path ...string) []string {
	t.Helper()
	node := schemaAt(t, doc, path...)
	props, ok := node["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s declares no properties", schemaPath(path))
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// schemaClosed reports whether the object the schema declares at path refuses a
// member it does not name.
func schemaClosed(t *testing.T, doc map[string]any, path ...string) bool {
	t.Helper()
	node := schemaAt(t, doc, path...)
	closed, ok := node["additionalProperties"].(bool)
	return ok && !closed
}

// schemaAt is the schema node at path, which is a list of member names to walk
// through.
func schemaAt(t *testing.T, doc map[string]any, path ...string) map[string]any {
	t.Helper()
	node := doc
	for _, key := range path {
		next, ok := node[key].(map[string]any)
		if !ok {
			t.Fatalf("%s: the schema has no object there", schemaPath(path))
		}
		node = next
	}
	return node
}

// fileMode is a file's permission bits.
func fileMode(path string) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}

func schemaPath(path []string) string {
	if len(path) == 0 {
		return "the schema"
	}
	return strings.Join(path, ".")
}
