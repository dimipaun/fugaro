package sharedjob

import (
	"slices"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// This file is the scaffolding of C1, and it is the one part of this package
// that goes away: I1 wires the sides and deletes this file and its test
// (internal/sharedjob/stub_test.go), which is why every stub lives here and
// nothing the sides keep does.
//
// Each stub returns the shape it is named for and decides nothing: it reads no
// policy, admits no repository, refuses nothing, resolves no profile and
// copies no list it was not handed. That is the whole of what a stub may do
// while S1 to S4 are written in parallel — the four sides call a signature
// that exists so that each compiles and its own tests run, and none of them
// can pass on a decision no side has made, because nothing here makes one.

// StubPairing is the shape of a pairing and nothing else: it carries the run
// identity and the profile it was called with and leaves the verdict
// undecided, which is a refusal (Pairing's zero value). A side that read it as
// an admission would be reading a decision out of a stub that made none.
func StubPairing(ident RunIdentity, profile string) (Pairing, error) {
	return Pairing{Identity: ident, Profile: profile}, nil
}

// StubJobChoice is the shape of a job choice and nothing else: it carries the
// repository and the workflow it was called with and resolves no profile,
// which for JobChoice is the per-repository job. The stub adds nothing to the
// two facts it is given, and it reads no policy to find the default profile of
// the repository's provider.
func StubJobChoice(repo, workflow string) (JobChoice, error) {
	return JobChoice{Repo: repo, Workflow: workflow}, nil
}

// StubBitbucketProfile is the shape of a Bitbucket profile and nothing else:
// it projects the name the caller holds the profile under and the published
// profile's own facts, and judges none of them. The account axis is the one
// derivation it makes, through config.ProjectJobAccounts, because carrying that
// axis is what the projection declares and not a rule of the stub's own.
// A profile the deployer must refuse (one mounting the Bitbucket token, say) is
// projected like any other — the stub applies no rule and reads no policy
// document, and the two lists are copies, so a side that edited the shape it
// was handed would not be editing the policy's.
func StubBitbucketProfile(name string, pr projectpolicy.Profile) BitbucketProfile {
	return BitbucketProfile{
		Name:         name,
		Provider:     pr.Provider,
		Accounts:     config.ProjectJobAccounts(pr.JobProfile),
		AllowRepos:   slices.Clone(pr.AllowRepos),
		Secrets:      slices.Clone(pr.Secrets),
		CPU:          pr.CPU,
		Memory:       pr.Memory,
		TimeoutClass: pr.TimeoutClass,
	}
}
