package config

// DefaultBaseBranch is the base branch of a repository that names none.
const DefaultBaseBranch = "main"

// The sources of a base branch (EffectiveBaseBranch): the wording a message
// prints for the layer that named the value.
const (
	BaseBranchSourceFile    = "fugaro.yaml git.base_branch"
	BaseBranchSourceRepo    = "local config repos.<r>.base_branch"
	BaseBranchSourceProject = "project base_branch"
)

// EffectiveBaseBranch is the base branch the repository repo runs against,
// and which layer named it: the checkout's committed fugaro.yaml when the
// file sets git.base_branch, else the local config's repos.<r>.base_branch,
// else the project's base_branch (the installation config's top-level key,
// read from the published policy when a machine's own config does not set
// one), else main.
//
// fileSets is whether the file's text writes the key at all
// (Resolution.SourceOf("git.base_branch") == SourceRepo), never a
// comparison against DefaultBaseBranch: a resolved config fills main in
// (applyDefaults), so a file that sets nothing and one that sets main look
// alike once resolved — and the two must resolve differently, since a file
// that sets nothing takes the project's default while one that sets main
// means main even where the project uses another branch.
func EffectiveBaseBranch(file string, fileSets bool, repo, project string) (branch, source string) {
	switch {
	case fileSets && file != "":
		return file, BaseBranchSourceFile
	case repo != "":
		return repo, BaseBranchSourceRepo
	case project != "":
		return project, BaseBranchSourceProject
	}
	return DefaultBaseBranch, SourceDefault
}
