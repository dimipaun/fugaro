package cli

import (
	"errors"
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

// flowLayer is a project layer whose profile writes the removed keys in flow
// style — `svc: { … image: { apt: [libvips-dev] } }` — which is a document
// yamledit has no edit for: it splices a member out of a flow mapping only
// while the member's value is a plain value, and this one's is a collection.
// The removal falls back to the document's own tree for it, and this is the
// document both sides of the migration are measured on.
const flowLayer = `version: 1
project: aurora
gcp_project: proj-1234
profiles:
  svc: { commands: { build: sh build.sh }, image: { apt: [libvips-dev] } }
default_profile: svc
`

// flowLayerMigrated is flowLayer as the removal writes it: the text editor has
// no splice for a member whose value is a collection, so the document is written
// from its own tree — the key out, the rest of it as the owner wrote it, its
// flow style and its spacing the tree's own.
const flowLayerMigrated = `version: 1
project: aurora
gcp_project: proj-1234
profiles:
  svc: {commands: {build: sh build.sh}}
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
// the four states a project layer is found in. The keys the repository-image
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
		// The object was published by a run that already took the keys out —
		// or by another machine's — and this machine's file still holds them:
		// the file is the whole of the work left, and a run that records the
		// generation and returns leaves it holding keys every reader refuses.
		{"the object already lost them and the file still holds them", migratedLayer, previousLayer, migratedLayer},
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

// TestUpdateMigratesAFlowStyledLayer: the removal's convergence on the document
// it has no splice for — `svc: { … image: { apt: […] } }`, the shape a release
// before this one published. The object is what every repository of the project
// resolves against and the local file is beside it, so neither may be left
// holding a key every reader refuses: the keys go out of the document's own tree
// and `fugaro update` alone writes both sides, on every state they are found in.
func TestUpdateMigratesAFlowStyledLayer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		local  string // "" writes no local layer file
		record string
	}{
		{"both sides hold them", flowLayer, "0"},
		{"there is no local layer file to seed", "", ""},
		{"the local file is from another generation", migratedLayer, "9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newLayerRig(t, flowLayer)
			path := localLayerPath(t)
			if tc.local != "" {
				recordLayer(t, tc.local, tc.record)
			}

			out, errOut, err := executeStdin(t, "", "update", "--yes")
			if err != nil {
				t.Fatalf("fugaro update left the published project layer holding a key every reader refuses: %v\n%s%s", err, out, errOut)
			}
			all := out + errOut
			for _, want := range []string{"profiles.svc.image.apt", "repository-image removal", "written from its own tree"} {
				if !strings.Contains(all, want) {
					t.Errorf("the run does not say %q:\n%s", want, all)
				}
			}
			if got, ok := layerObject(t, r); !ok || got != flowLayerMigrated {
				t.Errorf("the published object is not the layer the removal wrote from its tree:\n%q", got)
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil || string(data) != flowLayerMigrated {
				t.Errorf("the local layer file is not the layer the object now holds:\n%s%v", data, rerr)
			}
			if _, ps := config.ParseProjectLayer(data, config.LayerAnchor{Project: "aurora", GCPProject: "proj-1234"}); len(ps) > 0 {
				t.Errorf("the layer this run wrote is still refused: %v", ps)
			}
			if tc.local != "" {
				if matches, _ := filepath.Glob(path + ".bak-*"); len(matches) == 0 {
					t.Error("the local layer file was rewritten and no copy of it was kept")
				}
			}
		})
	}
}

// TestUpdateStopsWhenAPublishWouldLeaveAFlowStyledObject: the flow-styled object
// as the baseline of a publish. The local layer file is a document this Fugaro
// reads and it drops a command the object carried, which is an executable change
// the gate refuses (L7) — and `fugaro update` has no flag to bless it. The run
// stops with a non-zero error instead of reporting a converge, and the object is
// left as it is rather than written from a layer nobody reviewed.
func TestUpdateStopsWhenAPublishWouldLeaveAFlowStyledObject(t *testing.T) {
	r := newLayerRig(t, flowLayer)
	recordLayer(t, localOnlyMigrated, "0")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("fugaro update published a layer that drops a command of the object it replaces: %v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(out+errOut+err.Error(), "--executable-changes") {
		t.Errorf("the refusal does not name the flag that reviews such a write:\n%s%s", out, errOut)
	}
	if got, ok := layerObject(t, r); !ok || got != flowLayer {
		t.Errorf("the object was written from a layer this run may not publish: %q", got)
	}
}

// TestUpdateRefusesAPublishedLayerTheRemovalCannotLeaveValid: the state on the
// object side that no write of this run answers — every key of the profile its
// `default_profile` names is a removed one, so the document the removal makes is
// not a project layer this Fugaro reads. The object is the layer every
// repository of the project resolves against, so the run stops with a non-zero
// error rather than skipping the stage: a skip would report an installation
// converged that no repository of it can use.
//
// The remedy it names has to be one the reader can carry out from the state they
// are in, which is why the file to publish is asserted and not only the command:
// the seed state has no local layer file, so `fugaro config publish` on its
// defaulted path refuses with the command that sent the reader here, and a
// refusal that named that command alone would send them round the loop.
func TestUpdateRefusesAPublishedLayerTheRemovalCannotLeaveValid(t *testing.T) {
	const onlyRemovedKeysFlow = `version: 1
project: aurora
gcp_project: proj-1234
profiles:
  svc: {image: {apt: [libvips-dev]}}
default_profile: svc
`
	for _, tc := range []struct{ name, local, record string }{
		{"there is no local layer file to seed", "", ""},
		{"the local file is from another generation", migratedLayer, "9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newLayerRig(t, onlyRemovedKeysFlow)
			if tc.local != "" {
				recordLayer(t, tc.local, tc.record)
			}

			out, errOut, err := executeStdin(t, "", "update", "--yes")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("fugaro update reported a project layer converged that every reader refuses: %v\n%s%s", err, out, errOut)
			}
			all := out + errOut + err.Error()
			for _, want := range []string{"profiles.svc.image.apt", "default_profile", "config publish --executable-changes FILE"} {
				if !strings.Contains(all, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, all)
				}
			}
			if got, ok := layerObject(t, r); !ok || got != onlyRemovedKeysFlow {
				t.Errorf("the object was written from a layer this Fugaro refuses: %q", got)
			}
			data, rerr := os.ReadFile(localLayerPath(t))
			switch {
			case tc.local == "" && !errors.Is(rerr, os.ErrNotExist):
				t.Errorf("a local layer file was seeded from an object this Fugaro does not read: %v", rerr)
			case tc.local != "" && (rerr != nil || string(data) != tc.local):
				t.Errorf("this machine's layer file was written from an object this Fugaro does not read: %s%v", data, rerr)
			}
		})
	}
}

// TestRemovedKeysOut pins the strip itself: what it takes out, what it leaves
// byte for byte, the documents it refuses to write, and the two states that are
// neither — a key no splice of yamledit measures, which is taken out of the
// document's own tree instead, and a document with nothing to take out at all.
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

	t.Run("a key no splice of the editor measures is taken out of the tree", func(t *testing.T) {
		r := removedKeysOut([]byte(flowLayer), lc)
		if r == nil || r.unwritable() {
			t.Fatalf("the removal made no writable document of a flow-styled layer: %+v", r)
		}
		if !r.Reencoded {
			t.Error("the removal does not report that the document was written from its own tree")
		}
		if len(r.Keys) != 1 || r.Keys[0].Path() != "profiles.svc.image.apt" {
			t.Errorf("the keys the removal took out are %v", r.Keys)
		}
		if string(r.Data) != flowLayerMigrated {
			t.Errorf("the document the tree write left is not the layer minus the key:\n%s", r.Data)
		}
	})

	t.Run("the comments a flow-style document carries survive the tree write", func(t *testing.T) {
		const commented = "version: 1\nproject: aurora\ngcp_project: proj-1234\nprofiles:\n  svc: {commands: {build: sh build.sh}, image: {apt: [libvips-dev]}} # why this package\ndefault_profile: svc\n"
		r := removedKeysOut([]byte(commented), lc)
		if r == nil || r.unwritable() || !r.Reencoded {
			t.Fatalf("the removal made no writable document of a commented flow-styled layer: %+v", r)
		}
		if !strings.Contains(string(r.Data), "# why this package") {
			t.Errorf("the tree write dropped a comment of the document's:\n%s", r.Data)
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

// TestTheMigrateStateNamesEveryWriteItMakes: the migrate state rewrites the
// published object in place and this machine's layer file with it, in one apply
// behind one confirmation — migrateLocal asks nothing of its own when there is
// an object half — so its plan, its status, the sentence it is confirmed with
// and the report of what it did all name the file too. A sentence naming only
// the object would rewrite the owner's own file behind a confirmation that
// never mentioned it, which is the one thing this stage does not do.
func TestTheMigrateStateNamesEveryWriteItMakes(t *testing.T) {
	const path = "/home/x/.config/fugaro/config.yaml"
	keys := []layerRemovedKey{{Profile: "svc", Key: "image.apt"}}
	removal := func() *layerRemoval { return &layerRemoval{Keys: keys} }

	both := &layerPlan{path: path, migrate: true, objectRemoval: removal(), localRemoval: removal()}
	for what, got := range map[string]string{
		"the plan":         both.planDetail(),
		"the status":       both.migrateDetail(),
		"the confirmation": both.fileRewriteWhat(layerMigrateWhat),
	} {
		if !strings.Contains(got, path) {
			t.Errorf("%s does not name the file the same apply rewrites: %s", what, got)
		}
	}
	if got := both.rewroteWhat(); !strings.Contains(got, "local layer file") || !strings.Contains(got, "published project layer") {
		t.Errorf("the report names one of the two sides this apply wrote: %s", got)
	}

	objectOnly := &layerPlan{path: path, migrate: true, objectRemoval: removal()}
	for what, got := range map[string]string{
		"the plan":         objectOnly.planDetail(),
		"the status":       objectOnly.migrateDetail(),
		"the confirmation": objectOnly.fileRewriteWhat(layerMigrateWhat),
	} {
		if strings.Contains(got, "local layer file") {
			t.Errorf("%s names a file this run does not rewrite: %s", what, got)
		}
	}
	if got := objectOnly.rewroteWhat(); strings.Contains(got, "local layer file") {
		t.Errorf("the report names a file this run did not rewrite: %s", got)
	}

	fileOnly := &layerPlan{path: path, migrate: true, localRemoval: removal()}
	if got := fileOnly.migrateDetail(); strings.Contains(got, "published project layer") {
		t.Errorf("the status names an object this run does not write: %s", got)
	}
	if got := fileOnly.rewroteWhat(); strings.Contains(got, "published project layer") {
		t.Errorf("the report names an object this run did not write: %s", got)
	}
}
