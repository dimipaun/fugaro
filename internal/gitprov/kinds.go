package gitprov

// Provider kinds, as git.provider names them in fugaro.yaml.
const (
	KindGitHub    = "github"
	KindBitbucket = "bitbucket"
)

// Kinds lists the forge kinds a repository may name in git.provider, so that
// "which forges does Fugaro know" has one answer: a switch on a kind names the
// constants, and a message that has to spell the choice out joins this list
// rather than writing the kinds down again.
//
// It is exported and mutable because it is a list, not a set: nothing here
// guards against a caller appending to it, and a caller that needs a copy
// (the order it names them in is its own) makes one.
var Kinds = []string{KindGitHub, KindBitbucket}
