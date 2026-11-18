package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
)

// update_catalog_guard_test.go is the config guard of `fugaro update`
// (update_catalog_guard.go): the read-only stage that judges the local config's
// image catalog before the first cloud step, fixes the one entry whose fix is
// unambiguous, and names every other one.
//
// The fixture is the update door's own project fixture (newProjectFixture, the
// local config the door reads) with a catalog appended to it, and the catalog
// build's provider seam pointed at a fake repository (catalogOpen): the read the
// guard makes is the build's own, so the test drives it through the same seam.
// The credential is in the environment (EnvGitHubAppID and EnvGitHubAppKey), so
// no secret is read and no policy node is consulted — the two reads
// catalogProviderEnv would otherwise make, and neither of them is what these
// tests are about.

// The definition the fake repository holds, and the commit the default branch
// resolves to: a full SHA, because a resolved ref is held to one
// (catalog.resolveRef).
const (
	guardDef = "FROM example.invalid/base\nRUN true\n"
	guardSHA = "3f2b1c0d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
)

// guardPinnedYAML is the catalog the guard's own edit is about: one entry
// pinned to a branch, with comments around it — the bytes a splice has to leave
// where they are.
const guardPinnedYAML = `# The images this project's jobs run.
image_catalog:
  entries:
    - name: agent
      # The Dockerfile a job clones from.
      source:
        kind: dockerfile
        repo: acme/app
        path: docker/agent.Dockerfile
        branch: fugaro/2026-11-01-abc
`

// guardCleanYAML is the same entry with no pin, written in flow style: the
// catalog an update has nothing to say about.
const guardCleanYAML = `# The images this project's jobs run.
image_catalog:
  entries:
    - name: plain
      source: { kind: dockerfile, repo: acme/app, path: docker/plain.Dockerfile }
`

// guardSecretYAML is the definition a catalog entry may not carry (D2311): the
// entry's own bytes, so no repository is read for it at all.
const guardSecretYAML = `image_catalog:
  entries:
    - name: secretive
      source:
        kind: inline
        inline: |
          FROM example.invalid/base
          RUN --mount=type=secret,id=npm true
`

// guardState is a repository whose default branch holds guardDef at both paths
// and whose only branch is main: a read at any other ref fails, as a branch the
// repository no longer has does (fake.Provider.treeOf).
func guardState() fake.State {
	files := map[string]string{"docker/agent.Dockerfile": guardDef, "docker/plain.Dockerfile": guardDef}
	return fake.State{
		Branches: map[string]map[string]string{"main": files},
		Commits:  map[string]string{"": guardSHA},
		Trees:    map[string]map[string]string{guardSHA: files},
	}
}

// newGuardRig is the fixture the guard is called on, and the bytes of the file
// as the run found them.
func newGuardRig(t *testing.T, catalog string, state fake.State) (*migrationRig, string) {
	t.Helper()
	f := newProjectFixture(t, "")
	appendConfig(t, catalog)
	// The credential the seam resolves, from the environment: no Secret Manager
	// read and no policy node (catalog_provider_test's own fixture).
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, catalogAppKeyPlaceholder)
	p := &fake.Provider{Repo: "acme/app", State: state}
	was := openCatalogProvider
	openCatalogProvider = func([]string, func(string)) (string, gitprov.Opener, error) { return "", catalogOpen(p), nil }
	t.Cleanup(func() { openCatalogProvider = was })
	h := newMigrationRig(t, f, &initOptions{})
	before, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	return h, string(before)
}

// guardLines is what the guard printed, one line each, so a test can hold the
// whole of its output to one line per finding.
func guardLines(out string) []string {
	trimmed := strings.TrimSuffix(out, "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// guardRead is the file the run holds, after the guard ran.
func guardRead(t *testing.T, h *migrationRig) string {
	t.Helper()
	data, err := os.ReadFile(h.s.e.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestUpdateCatalogGuardRemovesAStalePin is the edit the stage makes itself: an
// entry pinned to a branch its repository no longer has, whose file the default
// branch also has, loses the pin — in the file, in one splice, with every other
// byte of it (the comment above the source among them) where it was written, and
// with the file the run replaced kept beside it. The line says which value went
// and where that copy is, so nothing the pin held is lost.
func TestUpdateCatalogGuardRemovesAStalePin(t *testing.T) {
	h, before := newGuardRig(t, guardPinnedYAML, guardState())

	h.s.e.guardCatalogEntries(context.Background())

	lines := guardLines(h.out.String())
	if len(lines) != 1 {
		t.Fatalf("the guard printed %d line(s), want one per finding:\n%s", len(lines), h.out.String())
	}
	line := lines[0]
	for _, want := range []string{"image catalog:", `entry "agent"`, "fugaro/2026-11-01-abc", "acme/app no longer has", "the pin was removed from"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line does not say %q: %s", want, line)
		}
	}
	// The pin is out of the file, and nothing else about it moved: the comment
	// above the source and every other byte are the bytes they were.
	after := guardRead(t, h)
	want := strings.Replace(before, "        branch: fugaro/2026-11-01-abc\n", "", 1)
	if after != want {
		t.Errorf("the file after the guard:\n%s\nwant the pin's line gone and nothing else moved:\n%s", after, want)
	}
	// The value that went is not lost: the file the run replaced is beside it,
	// named by the line, and holds the pin.
	at := strings.LastIndex(line, " is at ")
	if at < 0 {
		t.Fatalf("the line names no backup file: %s", line)
	}
	backup := strings.TrimSuffix(line[at+len(" is at "):], ")")
	if !strings.Contains(filepath.Base(backup), ".bak-") {
		t.Fatalf("the line names no backup file: %s", line)
	}
	kept, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("reading the file the line names: %v", err)
	}
	if string(kept) != before {
		t.Errorf("the backup is not the file the run replaced:\n%s", kept)
	}
	// And the config the run holds follows the file, so the run's own write of
	// it cannot put the pin back (unpinCatalogEntry).
	if got := h.s.e.lc.ImageCatalog.Entries[0].Source.Branch; got != "" {
		t.Errorf("the config the run holds still pins the entry to %q", got)
	}
}

// TestUpdateCatalogGuardNamesWhatItCannotFix is the other half: an entry this
// release will refuse whose fix is not the guard's to make — a pin whose branch
// has no such file (the default branch does not have it either), the issue's own
// entry, a definition that asks a build for a secret of its own, which the pin
// is not the reason for, and the same definition written inline. Each is named
// in one line with the edit, and the file is left exactly as it was.
func TestUpdateCatalogGuardNamesWhatItCannotFix(t *testing.T) {
	const asksForASecret = "FROM example.invalid/base\nRUN --mount=type=secret,id=npm true\n"
	// The repository the pinned branch has no file on, and whose default branch
	// does not have it either: only docker/other.Dockerfile is there.
	elsewhere := fake.State{
		Branches: map[string]map[string]string{"main": {"docker/other.Dockerfile": guardDef}},
		Commits:  map[string]string{"": guardSHA},
		Trees:    map[string]map[string]string{guardSHA: {"docker/other.Dockerfile": guardDef}},
	}
	// The issue's shape: the pinned branch has the file, and the file asks for a
	// secret — so the pin is not what this release refuses, and taking it out
	// would lose the revision for nothing.
	bothBranches := fake.State{
		Branches: map[string]map[string]string{"main": {"docker/agent.Dockerfile": asksForASecret}},
		Commits:  map[string]string{"": guardSHA, "fugaro/2026-11-01-abc": guardSHA},
		Trees:    map[string]map[string]string{guardSHA: {"docker/agent.Dockerfile": asksForASecret}},
	}
	for _, tc := range []struct {
		name, catalog string
		state         fake.State
		want          []string
	}{
		{
			name: "a pin whose branch has no such file", catalog: guardPinnedYAML, state: elsewhere,
			want: []string{"image catalog:", "catalog entry agent", "docker/agent.Dockerfile"},
		},
		{
			name: "a definition that asks for a secret", catalog: guardPinnedYAML, state: bothBranches,
			want: []string{"image catalog:", "catalog entry agent", "asks for a secret"},
		},
		{
			name: "an inline definition that asks for a secret", catalog: guardSecretYAML, state: guardState(),
			want: []string{"image catalog:", "catalog entry secretive", "line 2 of its Dockerfile asks for a secret"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, before := newGuardRig(t, tc.catalog, tc.state)
			h.s.e.guardCatalogEntries(context.Background())

			lines := guardLines(h.out.String())
			if len(lines) != 1 {
				t.Fatalf("the guard printed %d line(s), want one per finding:\n%s", len(lines), h.out.String())
			}
			for _, want := range append(tc.want, "image_catalog.entries[name:", h.s.e.path) {
				if !strings.Contains(lines[0], want) {
					t.Errorf("the line does not say %q: %s", want, lines[0])
				}
			}
			if after := guardRead(t, h); after != before {
				t.Errorf("the guard changed a file it only had to name:\n%s", after)
			}
		})
	}
}

// TestUpdateCatalogGuardIsSilentOnACleanCatalog: an entry this release reads
// prints nothing at all. The stage is measured by that as much as by the lines
// it does print — it runs before every update, and an update with nothing wrong
// with it is the ordinary one.
func TestUpdateCatalogGuardIsSilentOnACleanCatalog(t *testing.T) {
	// An inline entry alone: the entry's own bytes, no repository read at all.
	const clean = "image_catalog:\n  entries:\n    - name: plain\n      source:\n        kind: inline\n        inline: FROM example.invalid/base\n"
	for _, tc := range []struct {
		name, catalog string
		state         fake.State
	}{
		{"an inline entry", clean, guardState()},
		{"an entry with no pin", guardCleanYAML, guardState()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, before := newGuardRig(t, tc.catalog, tc.state)
			h.s.e.guardCatalogEntries(context.Background())
			if got := h.out.String(); got != "" {
				t.Errorf("the guard printed over a catalog it reads:\n%s", got)
			}
			if after := guardRead(t, h); after != before {
				t.Errorf("the guard changed a clean config:\n%s", after)
			}
		})
	}
}

// TestUpdateCatalogGuardIsSilentOnAnotherDoor: the stage is the update door's.
// `fugaro setup` reads the same file and has its own wizard, and a guard that
// ran there would report a catalog the person is in the middle of writing.
func TestUpdateCatalogGuardIsSilentOnAnotherDoor(t *testing.T) {
	h, before := newGuardRig(t, guardPinnedYAML, guardState())
	h.s.e.r.o.door = setupDoor.name
	h.s.e.guardCatalogEntries(context.Background())
	if got := h.out.String(); got != "" {
		t.Errorf("the guard ran on the setup door:\n%s", got)
	}
	if after := guardRead(t, h); after != before {
		t.Errorf("the guard changed the config on the setup door:\n%s", after)
	}
}

// TestUpdateConvergesWithACatalog is the door's own claim about the stage, and
// the wiring: the run is the converge fixture's (an installation one release
// behind, doctor clean afterwards — convergeOn asserts both), with a catalog in
// its local config, and the guard's line is part of that run's transcript. The
// pin is gone from the file the run left, which is the other half: the run's own
// write of the local config is made from the fixed copy, so a pin the guard took
// out does not come back with it.
func TestUpdateConvergesWithACatalog(t *testing.T) {
	r := newConvergeRig(t, currentNode())
	// The catalog goes into the file the door reads — the installation's own,
	// which this rig clears FUGARO_CONFIG for — and the installation is
	// re-registered over it, as the rig's other config writes do.
	r.appendConfig(t, guardPinnedYAML)
	registerInstallation(t, r.initRig)
	// The provider the guard reads the entry through, and the credential from
	// the environment: the converge rig's own app-key secret is never read.
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, catalogAppKeyPlaceholder)
	p := &fake.Provider{Repo: "acme/app", State: guardState()}
	was := openCatalogProvider
	openCatalogProvider = func([]string, func(string)) (string, gitprov.Opener, error) { return "", catalogOpen(p), nil }
	t.Cleanup(func() { openCatalogProvider = was })

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if n := strings.Count(out, "image catalog:"); n != 1 {
		t.Errorf("the run printed %d guard line(s), want the one the stale pin gets:\n%s", n, out)
	}
	lc := updatedConfig(t)
	if lc.ImageCatalog == nil || len(lc.ImageCatalog.Entries) != 1 {
		t.Fatalf("after one fugaro update the local config's catalog is %+v, want the entry it had", lc.ImageCatalog)
	}
	if got := lc.ImageCatalog.Entries[0].Source.Branch; got != "" {
		t.Errorf("the entry is still pinned to %q after the run, want the pin gone", got)
	}
}
