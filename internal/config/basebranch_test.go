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
