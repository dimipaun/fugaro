package bbtokens

import "testing"

// TestContractRecordForms is R1 read from both sides: the forms a type and a
// coverage agree in, and the ones they do not. The type is redundant for
// matching, so a record that states one scope and covers another is a record
// two sides would read differently — refused where it is written and where it
// is read, never resolved in the selector's favour.
//
// Every coverage here is spelled the one way a record has it: the node's
// provider-qualified form. The config's own two-segment allow_data_to spelling
// is refused (below), because a record spelled that way is one the service can
// neither build the legacy key's record in (R3) nor match a canonical
// repository with.
func TestContractRecordForms(t *testing.T) {
	agree := []struct {
		why string
		rec Record
	}{
		{
			why: "a repository token covers one workspace/repo",
			rec: Record{Name: "bitbucket-token--repo", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}},
		},
		{
			why: "a repository token may cover several repositories",
			rec: Record{Name: "bitbucket-token--pair", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app", "bitbucket/acme/api"}},
		},
		{
			why: "a workspace token covers the whole of one workspace",
			rec: Record{Name: "bitbucket-token--ws", Type: TypeWorkspace, Coverage: []string{"bitbucket/acme/*"}},
		},
		{
			why: "a project token covers one workspace and names its project",
			rec: Record{
				Name: "bitbucket-token--proj", Type: TypeProject,
				Coverage: []string{"bitbucket/acme/*"}, ProjectUUID: "proj-uuid", ProjectKey: "PROJ",
			},
		},
		{
			why: "the legacy key is untyped and covers the block's allow_repos list",
			rec: Record{
				Name: LegacyName, Type: TypeUntyped,
				Coverage: []string{"bitbucket/acme/app", "bitbucket/acme/*", "bitbucket/acme/api-*"},
			},
		},
		{
			why: "the legacy key of a node with no bitbucket_token block covers nothing",
			rec: Record{Name: LegacyName, Type: TypeUntyped},
		},
	}
	for _, tc := range agree {
		if err := tc.rec.Validate(); err != nil {
			t.Errorf("%s: Validate() = %v, want nil", tc.why, err)
		}
	}

	disagree := []struct {
		why string
		rec Record
	}{
		{
			why: "a repository token may not cover a whole workspace",
			rec: Record{Name: "bitbucket-token--repo", Type: TypeRepo, Coverage: []string{"bitbucket/acme/*"}},
		},
		{
			why: "a workspace token may not cover one repository",
			rec: Record{Name: "bitbucket-token--ws", Type: TypeWorkspace, Coverage: []string{"bitbucket/acme/app"}},
		},
		{
			why: "a project token may not cover one repository",
			rec: Record{
				Name: "bitbucket-token--proj", Type: TypeProject,
				Coverage: []string{"bitbucket/acme/app"}, ProjectUUID: "proj-uuid",
			},
		},
		{
			why: "a project token with no project UUID matches nothing",
			rec: Record{Name: "bitbucket-token--proj", Type: TypeProject, Coverage: []string{"bitbucket/acme/*"}},
		},
		{
			why: "a workspace token naming a project UUID is a project token",
			rec: Record{
				Name: "bitbucket-token--ws", Type: TypeWorkspace,
				Coverage: []string{"bitbucket/acme/*"}, ProjectUUID: "proj-uuid",
			},
		},
		{
			why: "a repository token naming a project UUID is a project token",
			rec: Record{
				Name: "bitbucket-token--repo", Type: TypeRepo,
				Coverage: []string{"bitbucket/acme/app"}, ProjectUUID: "proj-uuid",
			},
		},
		{
			why: "an untyped token naming a project UUID is a project token",
			rec: Record{
				Name: LegacyName, Type: TypeUntyped,
				Coverage: []string{"bitbucket/acme/*"}, ProjectUUID: "proj-uuid",
			},
		},
		{
			why: "no type outside the three is a token this build can serve",
			rec: Record{Name: "bitbucket-token--team", Type: "team", Coverage: []string{"bitbucket/acme/*"}},
		},
		{
			why: "a token covering nothing is not a token",
			rec: Record{Name: "bitbucket-token--empty", Type: TypeWorkspace},
		},
		{
			why: "a coverage entry is in the node's provider-qualified spelling",
			rec: Record{Name: "bitbucket-token--config", Type: TypeRepo, Coverage: []string{"acme/app"}},
		},
		{
			why: "a coverage entry is in the node's provider-qualified spelling",
			rec: Record{Name: LegacyName, Type: TypeUntyped, Coverage: []string{"acme/app", "acme/*"}},
		},
		{
			why: "a wildcard owner reaches every workspace (D464)",
			rec: Record{Name: "bitbucket-token--anyowner", Type: TypeWorkspace, Coverage: []string{"bitbucket/*/*"}},
		},
		{
			why: "a wildcard owner reaches every workspace (D464)",
			rec: Record{Name: "bitbucket-token--prefixws", Type: TypeWorkspace, Coverage: []string{"bitbucket/ac*/*"}},
		},
		{
			why: "a wildcard owner reaches every workspace (D464)",
			rec: Record{Name: "bitbucket-token--prefix", Type: TypeRepo, Coverage: []string{"bitbucket/ac*/app"}},
		},
		{
			why: "another provider's repository is not one this token reaches",
			rec: Record{Name: "bitbucket-token--other", Type: TypeRepo, Coverage: []string{"github/acme/app"}},
		},
		{
			why: "a bare wildcard is not a coverage pattern",
			rec: Record{Name: "bitbucket-token--wild", Type: TypeRepo, Coverage: []string{"*"}},
		},
		{
			why: "a pattern without a provider segment is not a coverage pattern",
			rec: Record{Name: "bitbucket-token--short", Type: TypeRepo, Coverage: []string{"*/*"}},
		},
		{
			why: "four wildcards in a name segment name no repository",
			rec: Record{Name: "bitbucket-token--stars", Type: TypeRepo, Coverage: []string{"bitbucket/acme/****"}},
		},
		{
			why: "more than two wildcards is a typo rather than an intent, in the type that checks the least",
			rec: Record{Name: LegacyName, Type: TypeUntyped, Coverage: []string{"bitbucket/acme/a*b*c*"}},
		},
		{
			why: "a workspace token covers one workspace, not a path inside one",
			rec: Record{Name: "bitbucket-token--nested", Type: TypeWorkspace, Coverage: []string{"bitbucket/acme/group/*"}},
		},
		{
			why: "a segment carries only the lower-case characters a repository name is made of",
			rec: Record{Name: "bitbucket-token--space", Type: TypeRepo, Coverage: []string{"bitbucket/acme/we b"}},
		},
		{
			why: "a segment carries only the lower-case characters a repository name is made of",
			rec: Record{Name: "bitbucket-token--upper", Type: TypeRepo, Coverage: []string{"bitbucket/ACME/app"}},
		},
		{
			why: "a segment carries only the lower-case characters a repository name is made of",
			rec: Record{Name: "bitbucket-token--escape", Type: TypeRepo, Coverage: []string{"bitbucket/acme/ap%2Fp"}},
		},
		{
			why: "an empty segment names nothing",
			rec: Record{Name: "bitbucket-token--emptyseg", Type: TypeWorkspace, Coverage: []string{"bitbucket/acme/"}},
		},
		{
			why: "a canonical repository never ends in .git",
			rec: Record{Name: "bitbucket-token--git", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app.git"}},
		},
		{
			why: "a typed token is not named the legacy key, which is the untyped record alone",
			rec: Record{Name: LegacyName, Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}},
		},
		{
			why: "an untyped record is the legacy key, so a node entry that leaves its type out of the family is refused",
			rec: Record{Name: "bitbucket-token--ci", Type: TypeUntyped, Coverage: []string{"bitbucket/acme/app"}},
		},
		{
			why: "a node entry that leaves its type out covers nothing at all rather than skipping the type and coverage check",
			rec: Record{Name: "bitbucket-token--ci", Type: TypeUntyped},
		},
		{
			why: "a token with no name is not a token",
			rec: Record{Name: "", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}},
		},
		{
			why: "a token with no name is not a token",
			rec: Record{Name: "", Type: TypeUntyped, Coverage: []string{"bitbucket/acme/app"}},
		},
		{
			why: "a name outside the family is one a model account could be spelled with",
			rec: Record{Name: "ci", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}},
		},
		{
			why: "a name outside the family is one a model account could be spelled with",
			rec: Record{Name: "bitbucket-token-ci", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}},
		},
		{
			why: "a name with nothing after the family's doubled dash names no token",
			rec: Record{Name: "bitbucket-token--", Type: TypeWorkspace, Coverage: []string{"bitbucket/acme/*"}},
		},
	}
	for _, tc := range disagree {
		if err := tc.rec.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want a refusal", tc.why)
		}
	}
}

// TestContractEmptyProjectUUIDNeverMatches is the constraint both sides of a
// project match rest on: an empty UUID is "unknown" on either side and never
// "the same unknown project", or a project token naming no project at all
// would serve every repository its lookup failed for.
func TestContractEmptyProjectUUIDNeverMatches(t *testing.T) {
	project := Record{
		Name: "bitbucket-token--proj", Type: TypeProject,
		Coverage: []string{"bitbucket/acme/*"}, ProjectUUID: "proj-uuid", ProjectKey: "PROJ",
	}
	if !project.MatchesProject("proj-uuid") {
		t.Error("a project token does not match the UUID it names")
	}
	if project.MatchesProject("") {
		t.Error("a project token matched an empty lookup UUID")
	}
	if project.MatchesProject("another-uuid") {
		t.Error("a project token matched another project's UUID")
	}

	unnamed := Record{Name: "bitbucket-token--proj", Type: TypeProject, Coverage: []string{"bitbucket/acme/*"}}
	if unnamed.MatchesProject("proj-uuid") || unnamed.MatchesProject("") {
		t.Error("a project token naming no UUID matched")
	}

	repo := Record{Name: "bitbucket-token--repo", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}}
	if repo.MatchesProject("proj-uuid") {
		t.Error("a repository token matched a project UUID, and only a project token has one")
	}
}
