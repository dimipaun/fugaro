package cli

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// image_map.go's tests: `fugaro image map set`, the one command that writes the
// project's repo-to-image map (D1906). What it writes is this machine's project
// config — the file `fugaro update` then publishes as the node's image_map — so
// the assertions here are made on the file a person and the publisher read.
//
// The command is in the managed plan's §3.1 set as a refusal (image_map_managed.go):
// its row is not "a session may write this under rules" but "no session writes
// this, and in a managed project the refusal is recorded". Both halves are
// tested, because the two are different worlds: an unmanaged one gets this
// command's own sentence, and a managed one gets the gate's.

// imageMapFile reads the fixture's project config back as the command left it.
func imageMapFile(t *testing.T) (string, map[string]string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the config after the write: %v", err)
	}
	return path, lc.ImageMap
}

// TestImageMapSetWritesTheMapping: one set writes one mapping into the project
// config, the file's other blocks are left alone, a second set of the same
// repository replaces its entry rather than adding a second one, and a second
// repository lands beside the first — the map is the project's whole map, so a
// write that replaced it would drop a mapping the operator made.
func TestImageMapSetWritesTheMapping(t *testing.T) {
	// Two repositories, because the map is written whole: a second mapping has
	// to land beside the first rather than in place of it.
	f := newCloudFixture(t)
	f.appendConfig(t, "  acme/sandbox: { provider: bitbucket, workflows: [web] }\n")
	f.appendConfig(t, catalogYAML)
	path := os.Getenv("FUGARO_CONFIG")

	// The first mapping: the file holds no map, so the note about the whole-map
	// replacement is what the operator reads before the write.
	out, errOut, err := execute(t, "image", "map", "set", "acme/app", "tools")
	if err != nil {
		t.Fatalf("image map set: %v\n%s", err, out)
	}
	if !strings.Contains(out, "acme/app") || !strings.Contains(out, "tools") {
		t.Errorf("image map set printed %q, want the mapping it wrote", out)
	}
	if !strings.Contains(errOut, "names no repo-to-image map") {
		t.Errorf("the first write says nothing about replacing the project's published map: %q", errOut)
	}
	_, got := imageMapFile(t)
	if want := map[string]string{"acme/app": "tools"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ImageMap = %v, want %v", got, want)
	}
	// The blocks the file already carried survive the write — userSave
	// marshals the whole config back out, so what a lost block looks like here
	// is a file that no longer holds it — and the catalog is one of them.
	after := readFileString(t, path)
	for _, keep := range []string{"image_catalog", "acme/sandbox", "proj-1234"} {
		if !strings.Contains(after, keep) {
			t.Errorf("the file after the write no longer holds %q:\n%s", keep, after)
		}
	}

	// A second set of the same repository replaces the entry, and the second
	// write says nothing about replacement: the file now holds a map.
	out, errOut, err = execute(t, "image", "map", "set", "acme/app", "base")
	if err != nil {
		t.Fatalf("the second image map set: %v\n%s", err, out)
	}
	if strings.Contains(errOut, "names no repo-to-image map") {
		t.Errorf("a file that already holds a map is reported as holding none: %q", errOut)
	}
	_, got = imageMapFile(t)
	if want := map[string]string{"acme/app": "base"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ImageMap = %v, want the one mapping replaced by %v", got, want)
	}

	// A second repository is added beside the first.
	if _, _, err := execute(t, "image", "map", "set", "acme/sandbox", "tools"); err != nil {
		t.Fatal(err)
	}
	_, got = imageMapFile(t)
	if want := map[string]string{"acme/app": "base", "acme/sandbox": "tools"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ImageMap = %v, want %v", got, want)
	}
}

// TestImageMapSetTakesACheckoutPath: the repository argument is read the way
// every --repo is — a path becomes the owner/name of its checkout's origin —
// and the key written is the canonical name, which is the spelling a run looks
// its own repository up by. A path outside the project's repository is refused
// before the file is touched, because a mapping nothing looks up is a line the
// operator would never see fail.
func TestImageMapSetTakesACheckoutPath(t *testing.T) {
	catalogFixture(t)
	dir := catalogCheckout(t, "version: 1\nproject: aurora\ngit: { provider: github }\n")
	out, _, err := execute(t, "image", "map", "set", dir, "tools")
	if err != nil {
		t.Fatalf("image map set against a checkout: %v\n%s", err, out)
	}
	if _, got := imageMapFile(t); !reflect.DeepEqual(got, map[string]string{"acme/app": "tools"}) {
		t.Errorf("ImageMap = %v, want the checkout's own repository mapped", got)
	}

	// A name that is not one of the project's repositories: refused, and the
	// file is left exactly as it was.
	path := os.Getenv("FUGARO_CONFIG")
	before := readFileString(t, path)
	_, _, err = execute(t, "image", "map", "set", "acme/other", "tools")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not one of project") {
		t.Fatalf("mapping a repository the project does not run: exit %d, err %v", ExitCode(err), err)
	}
	if readFileString(t, path) != before {
		t.Error("a refused mapping still wrote the config")
	}
}

// TestImageMapSetRefusesAMappingNothingWouldRead: the two mistakes a written
// mapping leaves nothing behind to notice, each refused before the file is
// touched — an entry this machine's catalog does not name (the map is published
// and a job would be deployed on an image no entry describes) and an entry name
// that is not a name (the same rule an entry's own name is held to).
func TestImageMapSetRefusesAMappingNothingWouldRead(t *testing.T) {
	catalogFixture(t)
	path := os.Getenv("FUGARO_CONFIG")
	before := readFileString(t, path)
	dir := t.TempDir()
	for _, tc := range []struct {
		name  string
		args  []string
		wants string
	}{
		{"an entry the catalog does not name", []string{"acme/app", "nope"}, "no entry named"},
		{"an entry name that is not one", []string{"acme/app", "API Image"}, "must be a catalog entry name"},
		{"a path that is no checkout", []string{dir, "tools"}, "not inside a git checkout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := execute(t, append([]string{"image", "map", "set"}, tc.args...)...)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("the refusal = %v, want it to name %q", err, tc.wants)
			}
			if readFileString(t, path) != before {
				t.Error("a refused mapping still wrote the config")
			}
		})
	}
}

// TestImageMapSetRefusesACodingAgentsSession: in a project this program cannot
// prove is managed, the command keeps the refusal it has always given a
// session — nothing is written, the exit class is the user's, and the sentence
// carries the command a person runs at their own terminal. Which image a
// project's jobs run is the project's own decision (D1906), and the entry's
// definition is written beside it by a session's own `fugaro image add`: a
// session that wrote both would be choosing where the project's code is built
// from, one ordinary-looking step at a time.
func TestImageMapSetRefusesACodingAgentsSession(t *testing.T) {
	catalogFixture(t)
	path := os.Getenv("FUGARO_CONFIG")
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "image", "map", "set", "acme/app", "tools")
	if err == nil {
		t.Fatalf("a coding agent's session wrote the project's image map:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want the user's class: a session asked for a step this program refuses it", ExitCode(err))
	}
	if !strings.Contains(err.Error(), "own terminal window") {
		t.Errorf("the refusal does not say where the write belongs: %v", err)
	}
	if !strings.Contains(err.Error(), "fugaro image map set acme/app tools") {
		t.Errorf("the refusal does not name the command to run instead: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("a session's refused write still changed the config")
	}
}

// TestImageMapSetManagedRefusesASession: in a managed project the session's
// write is judged by the project's own gate, refused in the gate's words, and
// the refusal is recorded — an owner auditing the project reads that a session
// tried to move where their repositories' images come from, and which rule
// refused it (D1014, D1025). The file is unchanged, and the rules the record
// carries are this command's own: what a session may not write here is the
// mapping itself, because the catalog entry it names is another document.
func TestImageMapSetManagedRefusesASession(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	r.appendConfig(t, catalogYAML)
	path := os.Getenv("FUGARO_CONFIG")
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "image", "map", "set", "acme/app", "tools")
	if err == nil {
		t.Fatalf("a managed project's gate admitted a session's map write:\n%s", out)
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the map write was not refused by the gate, so nothing says which control refused it: %v", err)
	}
	if strings.Contains(err.Error(), "own terminal window") {
		t.Error("this command's own refusal answered instead of the gate's, so a managed project read no mark")
	}
	if readFileString(t, path) != before {
		t.Error("the file changed under a refused mapping")
	}
	recs := r.audits(t)
	if len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the refusal left %+v, want exactly one refused record", recs)
	}
	if len(recs[0].Rules) == 0 || !strings.Contains(strings.Join(recs[0].Rules, " "), "another document") {
		t.Errorf("the refused record's rules (%v) do not say what refused", recs[0].Rules)
	}
}
