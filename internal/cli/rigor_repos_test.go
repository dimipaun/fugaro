package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// rigor_repos_test.go is RG3 of docs/repo-override-rulings (R6, R15 of the
// reconciliation, D3294 of the spec): the two homes D3237 gives a repository's
// rigor keys in the project, and the door R15 closes behind them.
//
//   - `fugaro rigor set <mode> --repo <owner/name>` with no --layer writes
//     `defaults.rigor.repos.<owner/name>`: the level the project states for one
//     repository of it, which a project area naming that repository in a member
//     and the repository's own file both outrank (LD4, D3294);
//   - `--area <glob> --name <name> --repo <owner/name>` writes one member of that
//     named rule — `{repo: <owner/name>, paths: [<glob>]}` — beside whatever else
//     the rule is made of (F13);
//   - `--layer repo` is refused for both keys, with the one sentence C1 holds for
//     the key in play, whose fix is the --repo form above (R15, F23);
//   - and the sensitive-direction fence keeps working for the new forms: a mode
//     below what that repository's runs resolve to today is refused in a coding
//     agent's session and written at the owner's own terminal with the sentence
//     saying what it lowers (D832, D1180).
//
// The world is the settings family's own: settingsWriteHome's hermetic
// installation (project aurora, gcp_project aurora-gcp) with this machine's
// project layer file written by the test at localcfg's one path. Every write
// here lands in that file — a repository's own fugaro.yaml states neither key
// since D3237 — so no test below needs a checkout.

// rigorReposFile writes this machine's project layer file with a rigor block
// below the three anchors: the shortest document these writes edit.
func rigorReposFile(t *testing.T, block string) string {
	t.Helper()
	return settingsManagedLayerFile(t, rigorAnchors+block)
}

// rigorLayerRigor is a layer file's rigor block, read with the layer's own
// loader rather than out of its text: the write's key is
// `defaults.rigor.repos.<owner/name>`, and a test that searched the bytes for
// the mode would pass on the layer's own default spelling it.
func rigorLayerRigor(t *testing.T, path string) config.LayerRigor {
	t.Helper()
	l, problems := config.ParseProjectLayer([]byte(readFileString(t, path)), rigorAnchor)
	if len(problems) > 0 {
		t.Fatalf("the written file is not a project layer: %s", problems[0].String())
	}
	return l.Defaults.Rigor
}

// rigorLayerRule is one rule of a layer file, by name, with the members it is
// made of: the rule a member write addresses, read the way the next reader of
// the file reads it.
func rigorLayerRule(t *testing.T, path, name string) rigor.Area {
	t.Helper()
	for _, a := range rigorLayerRigor(t, path).Areas {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("the layer carries no rule %q:\n%s", name, readFileString(t, path))
	return rigor.Area{}
}

// rigorMemberOf is one member of a rule, by the repository it names.
func rigorMemberOf(t *testing.T, area rigor.Area, repo string) rigor.Member {
	t.Helper()
	for _, m := range area.Members {
		if m.Repo == repo {
			return m
		}
	}
	t.Fatalf("the rule %q names no member %q: %v", area.Name, repo, area.Members)
	return rigor.Member{}
}

// TestRigorSetRepoWritesRigorRepos is the plan's red step: nothing wrote
// `defaults.rigor.repos` before this task (F13), so a project could state a
// level for each repository of it only by hand. The two forms and the fence are
// pinned here.
func TestRigorSetRepoWritesRigorRepos(t *testing.T) {
	settingsWriteHome(t)
	noAgentSession(t)
	noTerminal(t)

	// The whole-repository level: one entry of the map, written beside the
	// layer's own default rather than instead of it.
	path := rigorReposFile(t, "defaults:\n  rigor:\n    default: pro\n")
	out, _, err := execute(t, "rigor", "set", "critical", "--repo", "owner/repo-a")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := rigorLayerRigor(t, path)
	if got.Repos["owner/repo-a"] != rigor.Critical {
		t.Errorf("defaults.rigor.repos.owner/repo-a reads %q, want %q:\n%s", got.Repos["owner/repo-a"], rigor.Critical, readFileString(t, path))
	}
	if got.Default != rigor.Pro {
		t.Errorf("the write moved the layer's own default to %q:\n%s", got.Default, readFileString(t, path))
	}
	if !strings.Contains(out, "defaults.rigor.repos.owner/repo-a") {
		t.Errorf("the write does not name the entry it states:\n%s", out)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not name the stage that publishes the layer:\n%s", out)
	}
	rigorReadsBack(t, readFileString(t, path))

	// A second repository is a second entry: the map is keyed by the
	// repositories of the project, and one write never replaces another's.
	if out, _, err := execute(t, "rigor", "set", "startup", "--repo", "owner/repo-b"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got = rigorLayerRigor(t, path)
	if got.Repos["owner/repo-a"] != rigor.Critical || got.Repos["owner/repo-b"] != rigor.Startup {
		t.Errorf("the two entries do not both stand: %v\n%s", got.Repos, readFileString(t, path))
	}

	// The sensitive direction, for the entry as for the layer's default: a
	// session may not lower one repository's level, and the owner's own terminal
	// writes it and says what it lowers.
	path = rigorReposFile(t, "defaults:\n  rigor:\n    repos:\n      owner/repo-a: critical\n")
	before := readFileString(t, path)
	agentSession(t)
	_, _, err = execute(t, "rigor", "set", "poc", "--repo", "owner/repo-a")
	if err == nil || !strings.Contains(err.Error(), "coding agent's session") {
		t.Fatalf("a coding agent's session lowered a repository's level: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed the layer file")
	}
	noAgentSession(t)
	out, _, err = execute(t, "rigor", "set", "poc", "--repo", "owner/repo-a")
	if err != nil {
		t.Fatalf("the owner's own terminal was refused a lowering: %v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	if !strings.Contains(out, "lowers") {
		t.Errorf("the write does not say what it lowers:\n%s", out)
	}
	if got := rigorLayerRigor(t, path).Repos["owner/repo-a"]; got != rigor.POC {
		t.Errorf("the entry reads %q, want %q:\n%s", got, rigor.POC, readFileString(t, path))
	}

	// The member form: a rule the layer does not carry yet is added with the
	// member and the level together, and the rule is addressed by its name.
	path = rigorReposFile(t, "defaults:\n  rigor:\n    default: pro\n")
	out, _, err = execute(t, "rigor", "set", "critical", "--area", "docs/**", "--name", "docs", "--repo", "owner/repo-a")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	area := rigorLayerRule(t, path, "docs")
	if area.Level != rigor.Critical {
		t.Errorf("the new rule reads level %q, want %q:\n%s", area.Level, rigor.Critical, readFileString(t, path))
	}
	if member := rigorMemberOf(t, area, "owner/repo-a"); len(member.Paths) != 1 || member.Paths[0] != "docs/**" {
		t.Errorf("the member's paths read %v, want [docs/**]:\n%s", member.Paths, readFileString(t, path))
	}
	if !strings.Contains(out, "the rule docs") {
		t.Errorf("the write does not name the rule it wrote:\n%s", out)
	}
	rigorReadsBack(t, readFileString(t, path))

	// A rule the layer carries keeps what it is made of: the member added is one
	// entry beside the ones already there, and the other member's paths are not
	// what this write drops.
	path = rigorReposFile(t, "defaults:\n  rigor:\n    areas:\n      - name: docs\n        level: critical\n        members:\n          - repo: owner/repo-b\n            paths:\n              - cmd/**\n")
	if out, _, err = execute(t, "rigor", "set", "critical", "--area", "docs/**", "--name", "docs", "--repo", "owner/repo-a"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	area = rigorLayerRule(t, path, "docs")
	if len(area.Members) != 2 {
		t.Fatalf("the rule is made of %d members, want 2:\n%s", len(area.Members), readFileString(t, path))
	}
	if member := rigorMemberOf(t, area, "owner/repo-a"); len(member.Paths) != 1 || member.Paths[0] != "docs/**" {
		t.Errorf("the added member's paths read %v, want [docs/**]:\n%s", member.Paths, readFileString(t, path))
	}
	if member := rigorMemberOf(t, area, "owner/repo-b"); len(member.Paths) != 1 || member.Paths[0] != "cmd/**" {
		t.Errorf("the member that was there reads %v, want [cmd/**]:\n%s", member.Paths, readFileString(t, path))
	}
	rigorReadsBack(t, readFileString(t, path))

	// And the same rule a second time: the member is unioned and not repeated.
	if out, _, err = execute(t, "rigor", "set", "critical", "--area", "docs/**", "--name", "docs", "--repo", "owner/repo-a"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	area = rigorLayerRule(t, path, "docs")
	if len(area.Members) != 2 || len(rigorMemberOf(t, area, "owner/repo-a").Paths) != 1 {
		t.Errorf("a repeated write changed the rule: %v\n%s", area.Members, readFileString(t, path))
	}

	// The two refusals of the member form: the rule is addressed by its name, so
	// --area with --repo needs one, and a repository the layer cannot spell is
	// refused before a byte is built.
	before = readFileString(t, path)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"rigor", "set", "critical", "--area", "docs/**", "--repo", "owner/repo-a"}, "--name"},
		{[]string{"rigor", "set", "critical", "--area", "docs/**", "--name", "docs", "--repo", "owner/sub/repo"}, "owner/name"},
	} {
		out, _, err := execute(t, tc.args...)
		if err == nil {
			t.Fatalf("%v was accepted:\n%s", tc.args, out)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: the refusal does not name %q: %v", tc.args, tc.want, err)
		}
		if readFileString(t, path) != before {
			t.Errorf("%v: a refused write changed the layer file", tc.args)
		}
	}
}

// TestRigorSetLayerRepoRefused is R15's other half (F23): a repository's own
// fugaro.yaml states neither key any more, so the layer this command used to
// write them into is refused — with the one sentence C1 holds for the key in
// play, which is the sentence `settings set` prints for the same two keys and
// whose fix is this command's own --repo form. The refusal holds at the owner's
// own terminal and in a coding agent's session alike: nothing about who is
// asking makes a checkout a place these keys are stated, and it asks nothing
// (D1540).
func TestRigorSetLayerRepoRefused(t *testing.T) {
	settingsWriteHome(t)
	noTerminal(t)
	path := rigorReposFile(t, "defaults:\n  rigor:\n    default: pro\n")
	before := readFileString(t, path)

	for _, tc := range []struct {
		what string
		key  string
		args []string
	}{
		{
			"the level",
			"rigor.default",
			[]string{"rigor", "set", "critical", "--layer", "repo", "--repo", "owner/repo-a"},
		},
		{
			"a member",
			"rigor.areas",
			[]string{"rigor", "set", "critical", "--layer", "repo", "--repo", "owner/repo-a", "--area", "docs/**", "--name", "docs"},
		},
	} {
		k, ok := liftedKeyAt(tc.key)
		if !ok {
			t.Fatalf("C1's list carries no %s key", tc.key)
		}
		for _, session := range []bool{false, true} {
			if session {
				agentSession(t)
			} else {
				noAgentSession(t)
			}
			out, _, err := execute(t, tc.args...)
			if err == nil {
				t.Fatalf("%s: a --layer repo write of %s was accepted:\n%s", tc.what, tc.key, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("%s: the refusal is not a user error (%d): %v", tc.what, ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), k.Why) {
				t.Errorf("%s: the refusal is not C1's sentence for %s:\n%v\nwant it to carry:\n%s", tc.what, tc.key, err, k.Why)
			}
			assertNoPrompt(t, out)
			if readFileString(t, path) != before {
				t.Errorf("%s: a refused write changed the layer file", tc.what)
			}
		}
	}

	// The other two words the flag can carry are refused by the resolver, with
	// the sentences every other settings write reads: the key has one layer.
	noAgentSession(t)
	_, _, err := execute(t, "rigor", "set", "critical", "--layer", "local")
	if err == nil || !strings.Contains(err.Error(), "may not be set at --layer local") || !strings.Contains(err.Error(), "it may be set in project") {
		t.Errorf("--layer local is not refused with the layers the key has: %v", err)
	}
	_, _, err = execute(t, "rigor", "set", "critical", "--layer", "production")
	if err == nil || !strings.Contains(err.Error(), "a write lands in one of local, project, repo") {
		t.Errorf("a word that is no layer is not refused with the vocabulary: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("a refused write changed the layer file")
	}
}
