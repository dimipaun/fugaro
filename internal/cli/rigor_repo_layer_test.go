package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// rigor_repo_layer_test.go is M3's half of `fugaro rigor set`
// (docs/config-ownership/implementation-plan.md M3, D2612, D2614, D2617): the
// two rigor keys are also a repository's own, spelled bare in the checkout's
// fugaro.yaml, and `--layer repo` is the door to them — the same write
// `fugaro settings set --layer repo` makes, of a key this command owns.
//
// Four facts are this file's, one test each:
//
//   - the write lands in the checkout's own file and in the working tree: the
//     mode is live once the owner's branch is merged to the base branch and
//     nowhere before, nothing is committed, and the sentence says so (D1564,
//     the composite `models use` prints for its own write);
//   - a repository's write that drops the mode below what that repository's
//     runs resolve to TODAY — which is the project layer this machine holds,
//     since a repository's file is the narrower of the two — is the widening
//     step (D2617): a coding agent's session is refused it whatever the gate
//     says, and the owner's own terminal writes it with the sentence saying what it
//     lowers;
//   - T3's answer: the layer flag names a layer the key lacks and the write
//     says which layers that key has, and each layer the key does have is
//     written in that layer's own spelling — `defaults.rigor.default` in this
//     machine's layer file, `rigor.default` in a checkout's;
//   - the widening refusal is the session fence itself and not the flag: it
//     holds for every marker an agent sets and for both spellings of the key
//     (D1540: no command waits on input, so the fence is the refusal and never
//     a question — which is also why `rigor set` has no --yes to hand it).
//
// The world is the settings family's own (settingsWriteHome: project aurora,
// GCP project aurora-gcp, no marker set) plus a real git checkout of
// owner/repo-a with the working directory moved into it, so a write that lands
// in "the checkout the command runs in" is the one these tests read back.

// rigorRepoLayerYAML is a checkout's own file as these tests write it: the
// smallest document config.Parse accepts, carrying the project anchor the repo
// write is held to and no rigor block at all, which is the state a repository
// that never named a mode is in.
const rigorRepoLayerYAML = `version: 1
project: aurora
gcp_project: aurora-gcp
git:
  provider: github
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`

// rigorRepoBlock is the `rigor:` block a checkout's own file carries, read with
// the loader of that file rather than out of its text: the two keys the writes
// land on are `rigor.default` and `rigor.areas`, and a test that searched the
// bytes for "default:" would pass on the project layer's spelling of the same
// key.
func rigorRepoBlock(t *testing.T, path string) config.Rigor {
	t.Helper()
	cfg, problems := config.Parse([]byte(readFileString(t, path)))
	if len(problems) > 0 {
		t.Fatalf("the checkout's file does not read after the write: %s", problems[0].String())
	}
	if cfg.Rigor == nil {
		return config.Rigor{}
	}
	return *cfg.Rigor
}

// rigorRepoMode is the mode a checkout's own file sets.
func rigorRepoMode(t *testing.T, path string) rigor.Mode {
	t.Helper()
	return rigorRepoBlock(t, path).Default
}

// TestRigorSetRepoLayerWritesTheCheckout is the red step: `--layer repo` writes
// the mode into the checkout's own fugaro.yaml — the file every run of that
// repository reads from the base branch — and nowhere else. This machine's
// project layer file is not edited, and the write is not a commit, a stage or a
// push: it is a change in a working tree for the owner to review and carry to
// the base branch themselves (D1564).
//
// The layer file is seeded first and unchanged after, and the seeding is the
// reader's own precondition rather than the fixture's convenience: the mode this
// write is ranked against is that layer's, so a machine that does not hold it
// yet is refused outright (rigorMachineBlock, D2617). Seeding it at `pro` is
// also what makes the `critical` written below a raise.
func TestRigorSetRepoLayerWritesTheCheckout(t *testing.T) {
	settingsWriteHome(t)
	layerPath := rigorModeSet(t, rigorAnchors, "pro")
	beforeLayer := readFileString(t, layerPath)
	path := modelsWriteGitCheckout(t, "owner/repo-a", rigorRepoLayerYAML)
	dir := filepath.Dir(path)
	head := modelsUseGit(t, dir, "rev-parse", "HEAD")

	out, _, err := execute(t, "rigor", "set", "critical", "--layer", "repo", "--repo", "owner/repo-a")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	if got := rigorRepoMode(t, path); got != rigor.Critical {
		t.Errorf("the checkout's file reads %q, want %q:\n%s", got, rigor.Critical, readFileString(t, path))
	}
	if !strings.Contains(readFileString(t, path), "rigor:") {
		t.Errorf("the write did not land under the repository's own `rigor:` block:\n%s", readFileString(t, path))
	}
	if got := readFileString(t, layerPath); got != beforeLayer {
		t.Errorf("the --layer repo write reached this machine's project layer file:\n%s", got)
	}
	// D1564's sentence, and no `next: fugaro update` beside it: that stage
	// publishes the project layer file, and this write is not in it.
	if !strings.Contains(out, "owner/repo-a's working tree and nowhere else") {
		t.Errorf("the write does not say where the change is:\n%s", out)
	}
	if !strings.Contains(out, "merge that to the base branch") {
		t.Errorf("the write does not say how the mode becomes live:\n%s", out)
	}
	if strings.Contains(out, "next: fugaro update") {
		t.Errorf("a repository's own file was answered with the layer's next step:\n%s", out)
	}
	if got := modelsUseGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("the command committed: HEAD moved from %s to %s", head, got)
	}
	if staged := modelsUseGit(t, dir, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("the command staged %q", staged)
	}
	if changed := modelsUseGit(t, dir, "diff", "--name-only"); changed != "fugaro.yaml" {
		t.Errorf("the working tree's changed files are %q, want only fugaro.yaml", changed)
	}

	// The layer's other key, through the same door: `rigor.areas` is the second
	// of the two a repository spells, and the acceptance names both. The rule is
	// written whole — its glob, its name and its mode together — so the three
	// fields are read back through the checkout's loader and not searched for in
	// the bytes, and the same file is the only one the second write touches.
	out, _, err = execute(t, "rigor", "set", "critical", "--area", "src/**", "--name", "core", "--layer", "repo", "--repo", "owner/repo-a")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	rules := rigorRepoBlock(t, path).Areas
	if len(rules) != 1 || rules[0].Match != "src/**" || rules[0].Name != "core" || rules[0].Mode != rigor.Critical {
		t.Errorf("the checkout's rigor.areas reads %+v, want one rule src/** named core at critical:\n%s", rules, readFileString(t, path))
	}
	if !strings.Contains(out, "owner/repo-a's working tree and nowhere else") {
		t.Errorf("the area write does not say where the change is:\n%s", out)
	}
	if changed := modelsUseGit(t, dir, "diff", "--name-only"); changed != "fugaro.yaml" {
		t.Errorf("the two writes changed %q, want only fugaro.yaml", changed)
	}
}

// TestRigorSetRepoDefaultCannotLoosen: a repository's own mode is the narrower
// of the two layers a run reads, and the resolution it is compared against is
// therefore the one the PROJECT layer gives that repository — this machine's
// own layer file — and not the file's previous text (D2617's baseline, the
// widening half of the repository door).
//
// The refusal is the fence, not the value: at the owner's own terminal the same
// write lands and says what it lowers, because a repository that wants to run
// laxer than its project is making the repository's own decision (D21) — it is
// the owner's to make, and never a coding agent's.
func TestRigorSetRepoDefaultCannotLoosen(t *testing.T) {
	settingsWriteHome(t)
	rigorModeSet(t, rigorAnchors, "critical")
	path := modelsWriteGitCheckout(t, "owner/repo-a", rigorRepoLayerYAML)
	before := readFileString(t, path)

	agentSession(t)
	out, _, err := execute(t, "rigor", "set", "pro", "--layer", "repo", "--repo", "owner/repo-a")
	if err == nil {
		t.Fatalf("a coding agent's session lowered a repository's rigor mode:\n%s", out)
	}
	if !strings.Contains(err.Error(), "coding agent's session") {
		t.Errorf("the refusal does not name the session: %v", err)
	}
	if !strings.Contains(err.Error(), "fugaro rigor set pro --layer repo --repo owner/repo-a") {
		t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
	}
	if !strings.Contains(err.Error(), "set rigor.default to pro") {
		t.Errorf("the refusal does not name the key in the repository's own spelling: %v", err)
	}
	if !strings.Contains(out, "  + ") {
		t.Errorf("the refusal printed no diff of what it refused:\n%s", out)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the checkout's file:\n%s", got)
	}

	noAgentSession(t)
	noTerminal(t)
	out, _, err = execute(t, "rigor", "set", "pro", "--layer", "repo", "--repo", "owner/repo-a")
	if err != nil {
		t.Fatalf("the owner's own terminal was refused a lowering: %v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	if !strings.Contains(out, "this lowers owner/repo-a's rigor mode from critical to pro") {
		t.Errorf("the write does not say what it lowers and below what:\n%s", out)
	}
	if !strings.Contains(out, "every run of owner/repo-a's branches") {
		t.Errorf("the sentence is not about this repository's runs:\n%s", out)
	}
	if got := rigorRepoMode(t, path); got != rigor.Pro {
		t.Errorf("the checkout's file reads %q, want %q:\n%s", got, rigor.Pro, readFileString(t, path))
	}

	// And the baseline is the project layer and not the file: in a world whose
	// project layer resolves LOWER than what the checkout asks for, the same
	// write is an ordinary one and lands with no sentence about lowering.
	settingsWriteHome(t)
	rigorModeSet(t, rigorAnchors, "poc")
	path = modelsWriteGitCheckout(t, "owner/repo-a", rigorRepoLayerYAML)
	out, _, err = execute(t, "rigor", "set", "pro", "--layer", "repo", "--repo", "owner/repo-a")
	if err != nil {
		t.Fatalf("a repository raising its own mode above the project's was refused: %v\n%s", err, out)
	}
	if strings.Contains(out, "lowers") {
		t.Errorf("a write that lowers nothing below the project's mode reads as a widening:\n%s", out)
	}
	if got := rigorRepoMode(t, path); got != rigor.Pro {
		t.Errorf("the checkout's file reads %q, want %q", got, rigor.Pro)
	}
}

// TestRigorSetLayerFlag is T3 for these two keys (docs/contracts/
// config-ownership.md §6): every writer takes --layer, a layer the key lacks is
// refused naming the layers it HAS, and each layer it has is written in that
// layer's own spelling. The two rigor keys are settable at project and repo
// (config.Scopes) and nowhere else, so `local` is the layer that must name the
// two, and a word that is no layer at all is refused by the vocabulary before
// the row is consulted.
//
// The last half is the pairing: `--repo` is a repository, so it belongs with
// `--layer repo`, and a write that names one without the other is refused
// rather than silently landed in the layer file it defaulted to.
func TestRigorSetLayerFlag(t *testing.T) {
	settingsWriteHome(t)
	layerPath := rigorModeSet(t, rigorAnchors, "pro")
	path := modelsWriteGitCheckout(t, "owner/repo-a", rigorRepoLayerYAML)
	beforeRepo, beforeLayer := readFileString(t, path), readFileString(t, layerPath)

	_, _, err := execute(t, "rigor", "set", "critical", "--layer", "local")
	if err == nil {
		t.Fatal("a --layer local write of a key with two other layers was accepted")
	}
	if !strings.Contains(err.Error(), "rigor.default may not be set at --layer local") {
		t.Errorf("the refusal does not name the key and the layer: %v", err)
	}
	if !strings.Contains(err.Error(), "it may be set in project, repo") {
		t.Errorf("the refusal does not name the layers the key has: %v", err)
	}
	_, _, err = execute(t, "rigor", "set", "critical", "--layer", "production")
	if err == nil {
		t.Fatal("a --layer that is no layer at all was accepted")
	}
	if !strings.Contains(err.Error(), "a write lands in one of local, project, repo") {
		t.Errorf("the refusal does not name the layers a write lands in: %v", err)
	}
	if _, _, err := execute(t, "rigor", "set", "critical", "--repo", "owner/repo-a"); err == nil {
		t.Fatal("a --repo with no --layer repo landed in the project layer file")
	} else if !strings.Contains(err.Error(), "goes with --layer repo") {
		t.Errorf("the refusal does not pair the two flags: %v", err)
	}
	if got := readFileString(t, path); got != beforeRepo {
		t.Errorf("a refused write changed the checkout's file:\n%s", got)
	}
	if got := readFileString(t, layerPath); got != beforeLayer {
		t.Errorf("a refused write changed the layer file:\n%s", got)
	}

	// The project layer: the layer's own spelling, in the layer's own file.
	if out, _, err := execute(t, "rigor", "set", "critical", "--layer", "project"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, layerPath); !strings.Contains(got, "defaults:") || !strings.Contains(got, "default: critical") {
		t.Errorf("the project layer's spelling is not in the layer file:\n%s", got)
	}
	if got := readFileString(t, path); got != beforeRepo {
		t.Errorf("the --layer project write reached the checkout:\n%s", got)
	}

	// The repository layer: the same two keys, the other spelling, the other
	// file — and the layer file is left exactly as it was.
	if out, _, err := execute(t, "rigor", "set", "critical", "--layer", "repo"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := rigorRepoMode(t, path); got != rigor.Critical {
		t.Errorf("the checkout's file reads %q, want %q:\n%s", got, rigor.Critical, readFileString(t, path))
	}
	if got := readFileString(t, layerPath); !strings.Contains(got, "default: critical") {
		t.Errorf("the --layer repo write changed the layer file:\n%s", got)
	}
}

// rigorStaleLayerYAML is a machine's project layer file as an older installation
// of this project left it: the anchors name another GCP project, so the file is
// not this project's layer any more and nothing of it is read. It still carries
// a `critical` default, which is what makes the test's point: the mode below is
// not unknown because the file says nothing, but because the file cannot be
// read, and a reader that fell back to the built-in default would compare
// against a `pro` this repository does not resolve.
const rigorStaleLayerYAML = "version: 1\nproject: aurora\ngcp_project: aurora-old-gcp\ndefaults:\n  rigor:\n    default: critical\n"

// TestRigorSetRepoLayerRefusesAnUnreadableMachineLayer is the third state this
// fence has to answer, after "a lowering" and "not a lowering": the baseline
// itself cannot be read. A machine whose project layer file is absent, is not
// readable, or is not this project's file is one where what that repository
// resolves today cannot be told, and a reader that answered "no layer" with an
// empty block would resolve the built-in default `pro` — looser than what the
// repository's runs really resolve — so a lowering of them would read as no
// change at all, `Typed` would be false, and a coding agent's session would write
// a mode into a checkout the owner then merges (D2617).
//
// The three paths are walked in a coding agent's session and at the owner's own
// terminal, and a RAISE is walked beside the lowering: what refuses is the layer
// that does not read and not the widening class, which a terminal admits, so no
// session and no direction talks its way past a baseline nobody can name. The
// sentence names the file and the one command that seeds it, and the checkout is
// left byte-identical.
func TestRigorSetRepoLayerRefusesAnUnreadableMachineLayer(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(t *testing.T, path string) []string
	}{
		{
			name: "absent",
			seed: func(t *testing.T, path string) []string {
				t.Helper()
				if _, err := os.Stat(path); err == nil {
					t.Fatalf("the fixture wrote %s: this path is the machine that never seeded one", path)
				}
				return []string{"no local project layer file at " + path, "fugaro update seeds it from the published layer"}
			},
		},
		{
			name: "unreadable",
			seed: func(t *testing.T, path string) []string {
				t.Helper()
				return []string{"the local project layer file " + path + " is not read", unreadableLayerFile(t, path, "symlink")}
			},
		},
		{
			name: "another project's file",
			seed: func(t *testing.T, path string) []string {
				t.Helper()
				settingsManagedLayerFile(t, rigorStaleLayerYAML)
				return []string{path + " is not this project's layer file", "but it is read for GCP project"}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settingsWriteHome(t)
			path := modelsWriteGitCheckout(t, "owner/repo-a", rigorRepoLayerYAML)
			before := readFileString(t, path)
			want := tc.seed(t, localLayerPath(t))

			for _, session := range []struct {
				name string
				set  func(*testing.T)
			}{
				{"agent", agentSession},
				{"terminal", func(t *testing.T) { noAgentSession(t); noTerminal(t) }},
			} {
				session.set(t)
				for _, argv := range [][]string{
					{"rigor", "set", "pro", "--layer", "repo", "--repo", "owner/repo-a"},
					{"rigor", "set", "critical", "--layer", "repo", "--repo", "owner/repo-a"},
				} {
					line := strings.Join(argv, " ")
					out, _, err := execute(t, argv...)
					if err == nil {
						t.Fatalf("%s at a %s was ranked against a layer that does not read:\n%s", line, session.name, out)
					}
					if ExitCode(err) != ExitUserError {
						t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
					}
					for _, w := range want {
						if !strings.Contains(err.Error(), w) {
							t.Errorf("the refusal does not say %q: %v", w, err)
						}
					}
					assertNoPrompt(t, out)
					if got := readFileString(t, path); got != before {
						t.Fatalf("the refused write changed the checkout:\n%s", got)
					}
				}
			}
		})
	}
}

// TestRigorWideningRefusedInAnAgentSession is D2617 for this command, for both
// spellings of the two keys at once. The rule itself is the first half: the
// widening write is refused in a coding agent's session whatever the gate says
// — the diff is printed, the command to run at a terminal is named, the marker
// that refused is named, and the document is left byte-identical. The loop is
// over agentenv.Markers itself rather than a
// copy, so a marker the tree adds is one these writes are held to.
//
// The second half is what the refusal rests on: on the owner's own terminal the
// same edits are classified as widenings, and what says so is the sentence
// naming what each lowers. A write that widens nothing prints no such sentence,
// so the marker is carried by the edit and not by the command.
func TestRigorWideningRefusedInAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		for _, tc := range []struct {
			name  string
			argv  []string
			write func(t *testing.T) (path string, before string)
		}{
			{
				name: "project",
				argv: []string{"rigor", "set", "startup"},
				write: func(t *testing.T) (string, string) {
					path := rigorModeSet(t, rigorAnchors, "critical")
					return path, readFileString(t, path)
				},
			},
			{
				name: "repo",
				argv: []string{"rigor", "set", "startup", "--layer", "repo", "--repo", "owner/repo-a"},
				write: func(t *testing.T) (string, string) {
					rigorModeSet(t, rigorAnchors, "critical")
					path := modelsWriteGitCheckout(t, "owner/repo-a", rigorRepoLayerYAML)
					return path, readFileString(t, path)
				},
			},
		} {
			t.Run(marker+"/"+tc.name, func(t *testing.T) {
				settingsWriteHome(t)
				noAgentSession(t)
				path, before := tc.write(t)
				t.Setenv(marker, "1")

				out, _, err := execute(t, tc.argv...)
				if err == nil {
					t.Fatalf("the widening write landed in a coding agent's session:\n%s", out)
				}
				if ExitCode(err) != ExitUserError {
					t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
				}
				if !strings.Contains(err.Error(), marker) {
					t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
				}
				if !strings.Contains(err.Error(), "fugaro rigor set startup") {
					t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
				}
				if !strings.Contains(out, path) || !(strings.Contains(out, "  + ") || strings.Contains(out, "  - ")) {
					t.Errorf("the refusal does not print the diff of what it refused:\n%s", out)
				}
				if got := readFileString(t, path); got != before {
					t.Errorf("the file changed in a coding agent's session:\n%s", got)
				}
			})
		}
	}

	// The owner's own terminal: the same edits land, and each says what it
	// lowers. The two writes are the two spellings of one classification.
	settingsWriteHome(t)
	noAgentSession(t)
	noTerminal(t)
	layerPath := rigorModeSet(t, rigorAnchors, "critical")
	out, _, err := execute(t, "rigor", "set", "startup")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "lowers defaults.rigor.default from critical to startup") {
		t.Errorf("the layer write does not say what it lowers:\n%s", out)
	}
	if got := readFileString(t, layerPath); !strings.Contains(got, "default: startup") {
		t.Errorf("the owner's lowering did not land:\n%s", got)
	}

	path := modelsWriteGitCheckout(t, "owner/repo-a", rigorRepoLayerYAML)
	// A mode ABOVE the project layer's is not a widening, and the write says
	// nothing about lowering: the classification is the edit's, not the
	// command's.
	out, _, err = execute(t, "rigor", "set", "critical", "--layer", "repo", "--repo", "owner/repo-a")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "lowers") {
		t.Errorf("a write that lowers nothing reads as a widening:\n%s", out)
	}
	if got := rigorRepoMode(t, path); got != rigor.Critical {
		t.Errorf("the checkout's file reads %q, want %q", got, rigor.Critical)
	}
}
