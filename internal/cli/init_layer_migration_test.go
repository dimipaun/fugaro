package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// previousLayer is a project layer as the release before this one published it:
// profile svc carries the keys the repository-image removal refuses (image.apt,
// image.tools, rebuild.check) beside the settings that stay. Every reader
// refuses this document by name (config.ParseProjectLayer), so a project that
// published it has no repository that can run until `fugaro update` has taken
// those keys out of it.
const previousLayer = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { provider: github }
  agent: { auth: api-key }
profiles:
  svc:
    commands: { build: sh build.sh, test: sh test.sh }
    image:
      apt: [libvips-dev]
      tools: { node: "24.19.0" }
    rebuild:
      check: "off"
default_profile: svc
`

// migratedLayer is previousLayer as this run writes it: the removed keys gone,
// and everything else — profile, commands, a comment — as the owner left it.
const migratedLayer = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { provider: github }
  agent: { auth: api-key }
profiles:
  svc:
    commands: { build: sh build.sh, test: sh test.sh }
default_profile: svc
`

// localOnlyLayer is the third state: a project whose layer was published
// never, and whose layer file the release before this one wrote with the keys
// the removal refuses. Its profile runs no commands — publishing a layer whose
// commands are new is the executable-key gate's own refusal (decision L7), and
// that gate is not this migration's to answer.
const localOnlyLayer = `version: 1
project: aurora
gcp_project: proj-1234
profiles:
  svc:
    resources: { cpu: 2 }
    image:
      apt: [libvips-dev]
    rebuild:
      check: "off"
default_profile: svc
`

// localOnlyMigrated is localOnlyLayer as the publish writes it.
const localOnlyMigrated = `version: 1
project: aurora
gcp_project: proj-1234
profiles:
  svc:
    resources: { cpu: 2 }
default_profile: svc
`

// TestUpdateMigratesAPreviousReleaseLayer: the removal's own convergence, on
// the three states a project layer is found in. The keys the repository-image
// removal refuses go out of the published object (in place: the project is
// never left with no layer), out of this machine's local layer file, or out of
// both, with `fugaro update` alone and no second command.
func TestUpdateMigratesAPreviousReleaseLayer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		published string
		local     string // "" writes no local layer file
		want      string
	}{
		{"both sides hold them", previousLayer, previousLayer, migratedLayer},
		{"only the published object holds them", previousLayer, "", migratedLayer},
		{"only the local layer file holds them", "", localOnlyLayer, localOnlyMigrated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newLayerRig(t, tc.published)
			path := localLayerPath(t)
			if tc.local != "" {
				recordLayer(t, tc.local, "0")
			}

			out, errOut, err := executeStdin(t, "", "update", "--yes")
			if err != nil {
				t.Fatalf("fugaro update left a previous release's layer in place: %v\n%s%s", err, out, errOut)
			}
			all := out + errOut
			if !strings.Contains(all, "profiles.svc.image.apt") || !strings.Contains(all, "repository-image removal") {
				t.Errorf("the run does not say which keys it took out:\n%s", all)
			}
			if got, ok := layerObject(t, r); !ok || got != tc.want {
				t.Errorf("the published object is not the layer without the removed keys:\n%q", got)
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("the local layer file is gone: %v", rerr)
			}
			if string(data) != tc.want {
				t.Errorf("the local layer file is not the layer without the removed keys:\n%s", data)
			}
			if _, ps := config.ParseProjectLayer(data, config.LayerAnchor{Project: "aurora", GCPProject: "proj-1234"}); len(ps) > 0 {
				t.Errorf("the layer this run wrote is still refused: %v", ps)
			}
			if tc.local != "" {
				// The owner's file was rewritten, so what it held is kept
				// beside it: a removal that cannot be read back afterwards is
				// not one they can judge.
				if !strings.Contains(all, "backup at") {
					t.Errorf("the run rewrote the local layer file without saying where the old one is:\n%s", all)
				}
				if matches, _ := filepath.Glob(path + ".bak-*"); len(matches) == 0 {
					t.Error("the local layer file was rewritten and no copy of it was kept")
				}
			}
		})
	}
}

// TestUpdateMigratesAPreviousReleaseLayerOnlyOnce: the migration is idempotent
// — the second update finds a layer this Fugaro reads, nothing to take out, and
// leaves both sides exactly as they are.
func TestUpdateMigratesAPreviousReleaseLayerOnlyOnce(t *testing.T) {
	r := newLayerRig(t, previousLayer)
	recordLayer(t, previousLayer, "0")
	if _, _, err := executeStdin(t, "", "update", "--yes"); err != nil {
		t.Fatalf("fugaro update: %v", err)
	}

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("the second fugaro update refused the layer the first one wrote: %v\n%s%s", err, out, errOut)
	}
	if got, ok := layerObject(t, r); !ok || got != migratedLayer {
		t.Errorf("the second run rewrote the published object:\n%q", got)
	}
	if data, _ := os.ReadFile(localLayerPath(t)); string(data) != migratedLayer {
		t.Errorf("the second run rewrote the local layer file:\n%s", data)
	}
	if strings.Contains(out+errOut, "repository-image removal") {
		t.Errorf("the second run says it migrated a layer it had already migrated:\n%s%s", out, errOut)
	}
}

// TestUpdateRefusesALayerTheRemovalCannotLeaveValid: the one state the migration
// does not answer is a layer whose every other key was a removed one under the
// profile its default_profile names — the removal empties the profile, and a
// layer whose default_profile names no profile is not one a reader accepts.
// Nothing is published from it, and the refusal names the profile, which is the
// part the owner has to decide about.
func TestUpdateRefusesALayerTheRemovalCannotLeaveValid(t *testing.T) {
	const onlyRemovedKeys = `version: 1
project: aurora
gcp_project: proj-1234
profiles:
  svc:
    image: { apt: [libvips-dev] }
default_profile: svc
`
	r := newLayerRig(t, onlyRemovedKeys)
	recordLayer(t, onlyRemovedKeys, "0")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("fugaro update published a layer the removal cannot leave valid: %v\n%s%s", err, out, errOut)
	}
	all := out + errOut + err.Error()
	if !strings.Contains(all, "default_profile") && !strings.Contains(all, "profiles:") {
		t.Errorf("the refusal does not say what the removal left wrong with the layer:\n%s", all)
	}
	if got, ok := layerObject(t, r); !ok || got != onlyRemovedKeys {
		t.Errorf("the object was written from a layer this Fugaro refuses: %q", got)
	}
}

// TestRemovedKeysOut pins the strip itself: what it takes out, what it leaves
// byte for byte, and the two documents it refuses to write.
func TestRemovedKeysOut(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234"}

	t.Run("the profile keeps its other keys", func(t *testing.T) {
		r := removedKeysOut([]byte(previousLayer), lc)
		if r == nil {
			t.Fatal("no removal was made of a layer holding three removed keys")
		}
		if len(r.Problems) > 0 {
			t.Fatalf("the removal is not writable: %v", r.Problems)
		}
		if string(r.Data) != migratedLayer {
			t.Errorf("the strip is not the layer minus those keys:\n%s", r.Data)
		}
		if len(r.Keys) != 3 || r.Keys[0].Path() != "profiles.svc.image.apt" || r.Keys[2].Path() != "profiles.svc.rebuild.check" {
			t.Errorf("the keys removed are %v", r.Keys)
		}
		if len(r.Profiles) != 0 {
			t.Errorf("the strip dropped profiles it should have kept: %v", r.Profiles)
		}
	})

	t.Run("a comment beside a removed key", func(t *testing.T) {
		const commented = "version: 1\nproject: aurora\ngcp_project: proj-1234\n# the profile's own note\nprofiles:\n  svc:\n    commands: { build: sh build.sh }\n    # why this package\n    image: { apt: [libvips-dev] }\ndefault_profile: svc\n"
		r := removedKeysOut([]byte(commented), lc)
		if r == nil || len(r.Problems) > 0 {
			t.Fatalf("the strip refused a layer it should migrate: %v", r)
		}
		if !strings.Contains(string(r.Data), "# the profile's own note") {
			t.Errorf("the strip dropped a comment of the profile's:\n%s", r.Data)
		}
	})

	t.Run("nothing to take out", func(t *testing.T) {
		if r := removedKeysOut([]byte(testProjectLayer), lc); r != nil {
			t.Errorf("a layer with no removed key was rewritten: %+v", r)
		}
	})

	t.Run("a profile the removal empties", func(t *testing.T) {
		const emptied = "version: 1\nproject: aurora\ngcp_project: proj-1234\nprofiles:\n  api:\n    image: { apt: [libvips-dev] }\n  svc:\n    commands: { test: sh test.sh }\ndefault_profile: svc\n"
		r := removedKeysOut([]byte(emptied), lc)
		if r == nil || len(r.Problems) > 0 {
			t.Fatalf("the strip refused a layer whose other profile survives: %v", r)
		}
		if len(r.Profiles) != 1 || r.Profiles[0] != "api" {
			t.Errorf("the emptied profile is not reported: %v", r.Profiles)
		}
	})
}
