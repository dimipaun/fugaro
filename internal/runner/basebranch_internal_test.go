package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestResolveConfigTakesTheTasksBaseBranch pins where the project's default
// base branch reaches a run's configuration: a file that writes no
// git.base_branch of its own takes the task's, and the resolution says the
// value came from the project; a file that writes one keeps it, whatever the
// task carries.
func TestResolveConfigTakesTheTasksBaseBranch(t *testing.T) {
	const yaml = "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"
	const withKey = "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nworkflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"
	for _, tc := range []struct {
		name, file, base, want, wantSource string
	}{
		{"no key, no base", yaml, "", "main", config.SourceDefault},
		{"no key, the task's base", yaml, "master", "master", config.SourceProject},
		{"the file's own key beats the task's", withKey, "master", "develop", config.SourceRepo},
		{"the file's own main beats the task's", withKey, "develop", "develop", config.SourceRepo},
		{"a key the file wrote as main is still the file's", "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\nworkflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n", "master", "main", config.SourceRepo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := ResolveConfig([]byte(tc.file), nil, "aurora", tc.base)
			if len(out.Problems) > 0 {
				t.Fatal(problemsText(out.Problems))
			}
			if got := out.Cfg.Git.BaseBranch; got != tc.want {
				t.Errorf("git.base_branch = %q, want %q", got, tc.want)
			}
			if got := out.Res.SourceOf("git.base_branch"); got != tc.wantSource {
				t.Errorf("source = %q, want %q", got, tc.wantSource)
			}
		})
	}
}

// TestResolveConfigSumFollowsTheBaseBranch: the config's sum is what the run
// records and what a consumer compares, so a resolution that took the task's
// base must sum the config it produced — the sum of the file alone would name
// a configuration no run ever used.
func TestResolveConfigSumFollowsTheBaseBranch(t *testing.T) {
	const yaml = "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"
	plain := ResolveConfig([]byte(yaml), nil, "aurora", "")
	taken := ResolveConfig([]byte(yaml), nil, "aurora", "master")
	if taken.Res.ConfigSHA256 == plain.Res.ConfigSHA256 {
		t.Fatal("the sum did not move with the base branch")
	}
	if taken.Res.ConfigSHA256 != taken.Cfg.SHA256() {
		t.Errorf("sum = %s, the resolved config's is %s", taken.Res.ConfigSHA256, taken.Cfg.SHA256())
	}
	if plain.Res.ConfigSHA256 != plain.Cfg.SHA256() {
		t.Errorf("a resolution that took no base still changed the sum: %s, want %s", plain.Res.ConfigSHA256, plain.Cfg.SHA256())
	}
}

// TestResolveConfigLayerBaseIsOurs: a file plus the project layer, with the
// task's base taken, keeps the layer's own application and records the
// project source for the branch.
func TestResolveConfigLayerBaseIsOurs(t *testing.T) {
	const yaml = "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: github }\nworkflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"
	const layer = "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  agent:\n    review_rounds: 3\n"
	pl := &task.ProjectLayer{SHA256: config.LayerSum([]byte(layer)), YAML: layer}
	out := ResolveConfig([]byte(yaml), pl, "aurora", "master")
	if out.LayerErr != nil || len(out.Problems) > 0 || !out.Applied {
		t.Fatal(out.LayerErr, out.Problems, out.Applied)
	}
	if out.Cfg.Git.BaseBranch != "master" || out.Res.SourceOf("git.base_branch") != config.SourceProject {
		t.Errorf("git.base_branch = %q from %q, want master from %q", out.Cfg.Git.BaseBranch, out.Res.SourceOf("git.base_branch"), config.SourceProject)
	}
	if out.Res.ConfigSHA256 != out.Cfg.SHA256() {
		t.Errorf("sum = %s, the resolved config's is %s", out.Res.ConfigSHA256, out.Cfg.SHA256())
	}
}
