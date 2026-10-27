package config

import "testing"

// TestEffectiveBaseBranch pins the precedence of the four layers a base
// branch can come from, every layer winning and every empty combination:
// the repository's committed fugaro.yaml when it sets the key, else the
// local config's repos.<r>.base_branch, else the project's base_branch,
// else main.
func TestEffectiveBaseBranch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		file     string
		fileSets bool
		repo     string
		project  string
		want     string
		source   string
	}{
		{name: "the file alone", file: "develop", fileSets: true, want: "develop", source: BaseBranchSourceFile},
		{name: "the file beats the repo", file: "develop", fileSets: true, repo: "master", want: "develop", source: BaseBranchSourceFile},
		{name: "the file beats the project", file: "develop", fileSets: true, project: "trunk", want: "develop", source: BaseBranchSourceFile},
		{name: "the file beats both", file: "develop", fileSets: true, repo: "master", project: "trunk", want: "develop", source: BaseBranchSourceFile},
		{name: "an explicit main in the file wins", file: "main", fileSets: true, repo: "master", project: "trunk", want: "main", source: BaseBranchSourceFile},
		{name: "a file that does not set the key leaves the repo", file: "main", fileSets: false, repo: "master", want: "master", source: BaseBranchSourceRepo},
		{name: "the repo alone", repo: "master", want: "master", source: BaseBranchSourceRepo},
		{name: "the repo beats the project", repo: "master", project: "trunk", want: "master", source: BaseBranchSourceRepo},
		{name: "the project alone", project: "trunk", want: "trunk", source: BaseBranchSourceProject},
		{name: "nothing set", want: "main", source: SourceDefault},
		{name: "a set-but-empty file value does not win", file: "", fileSets: true, repo: "master", want: "master", source: BaseBranchSourceRepo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, source := EffectiveBaseBranch(tc.file, tc.fileSets, tc.repo, tc.project)
			if got != tc.want || source != tc.source {
				t.Errorf("EffectiveBaseBranch(%q, %v, %q, %q) = (%q, %q), want (%q, %q)",
					tc.file, tc.fileSets, tc.repo, tc.project, got, source, tc.want, tc.source)
			}
		})
	}
}

// TestSetsGitBaseBranch pins the question EffectiveBaseBranch's fileSets
// argument asks of a checkout's text: does it write git.base_branch at all.
// The value alone cannot answer it — a resolved config fills main in — so a
// caller that compared against main would read a file that sets nothing as a
// file that chose main, and a project on master would never be used.
func TestSetsGitBaseBranch(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want bool
	}{
		{name: "set", yaml: "git:\n  base_branch: develop\n", want: true},
		{name: "set to main", yaml: "git:\n  base_branch: main\n", want: true},
		{name: "beside another git key", yaml: "git:\n  provider: github\n  base_branch: master\n", want: true},
		{name: "absent", yaml: "git:\n  provider: github\n"},
		{name: "no git block", yaml: "version: 1\n"},
		{name: "an empty document", yaml: ""},
		{name: "explicit null", yaml: "git:\n  base_branch:\n"},
		{name: "another block's key", yaml: "agent:\n  base_branch: develop\n"},
		{name: "not a mapping", yaml: "version: 1\ngit: []\n"},
		{name: "merged in", yaml: "git:\n  <<: {base_branch: develop}\n", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SetsGitBaseBranch([]byte(tc.yaml)); got != tc.want {
				t.Errorf("SetsGitBaseBranch(%q) = %v, want %v", tc.yaml, got, tc.want)
			}
		})
	}
}
