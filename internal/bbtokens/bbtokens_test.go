package bbtokens

import "testing"

// TestContractRecordForms is R1 read from both sides: the forms a type and a
// coverage agree in, and the ones they do not. The type is redundant for
// matching, so a record that states one scope and covers another is a record
// two sides would read differently — refused where it is written and where it
// is read, never resolved in the selector's favour.
func TestContractRecordForms(t *testing.T) {
	agree := []struct {
		why string
		rec Record
	}{
		{
			why: "a repository token covers one workspace/repo",
			rec: Record{Name: "bitbucket-token--repo", Type: TypeRepo, Coverage: []string{"acme/app"}},
		},
		{
			why: "a repository token may cover several repositories",
			rec: Record{Name: "bitbucket-token--pair", Type: TypeRepo, Coverage: []string{"acme/app", "acme/api"}},
		},
		{
			why: "a workspace token covers the whole of one workspace",
			rec: Record{Name: "bitbucket-token--ws", Type: TypeWorkspace, Coverage: []string{"acme/*"}},
		},
		{
			why: "a project token covers one workspace and names its project",
			rec: Record{
				Name: "bitbucket-token--proj", Type: TypeProject,
				Coverage: []string{"acme/*"}, ProjectUUID: "proj-uuid", ProjectKey: "PROJ",
			},
		},
		{
			why: "the legacy key is untyped and covers whatever allow_data_to says",
			rec: Record{Name: LegacyName, Type: TypeUntyped, Coverage: []string{"acme/app", "acme/*"}},
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
			rec: Record{Name: "bitbucket-token--repo", Type: TypeRepo, Coverage: []string{"acme/*"}},
		},
		{
			why: "a workspace token may not cover one repository",
			rec: Record{Name: "bitbucket-token--ws", Type: TypeWorkspace, Coverage: []string{"acme/app"}},
		},
		{
			why: "a project token may not cover one repository",
			rec: Record{
				Name: "bitbucket-token--proj", Type: TypeProject,
				Coverage: []string{"acme/app"}, ProjectUUID: "proj-uuid",
			},
		},
		{
			why: "a project token with no project UUID matches nothing",
			rec: Record{Name: "bitbucket-token--proj", Type: TypeProject, Coverage: []string{"acme/*"}},
		},
		{
			why: "a workspace token naming a project UUID is a project token",
			rec: Record{
				Name: "bitbucket-token--ws", Type: TypeWorkspace,
				Coverage: []string{"acme/*"}, ProjectUUID: "proj-uuid",
			},
		},
		{
			why: "a repository token naming a project UUID is a project token",
			rec: Record{
				Name: "bitbucket-token--repo", Type: TypeRepo,
				Coverage: []string{"acme/app"}, ProjectUUID: "proj-uuid",
			},
		},
		{
			why: "no type outside the three is a token this build can serve",
			rec: Record{Name: "bitbucket-token--team", Type: "team", Coverage: []string{"acme/*"}},
		},
		{
			why: "a token covering nothing is not a token",
			rec: Record{Name: "bitbucket-token--empty", Type: TypeWorkspace},
		},
		{
			why: "a wildcard owner is not a coverage pattern (D464)",
			rec: Record{Name: "bitbucket-token--any", Type: TypeWorkspace, Coverage: []string{"*/*"}},
		},
		{
			why: "a pattern deeper than owner/name is not a repository",
			rec: Record{Name: "bitbucket-token--deep", Type: TypeRepo, Coverage: []string{"acme/app/sub"}},
		},
		{
			why: "a bare wildcard is not a coverage pattern",
			rec: Record{Name: "bitbucket-token--wild", Type: TypeRepo, Coverage: []string{"*"}},
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
		Coverage: []string{"acme/*"}, ProjectUUID: "proj-uuid", ProjectKey: "PROJ",
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

	unnamed := Record{Name: "bitbucket-token--proj", Type: TypeProject, Coverage: []string{"acme/*"}}
	if unnamed.MatchesProject("proj-uuid") || unnamed.MatchesProject("") {
		t.Error("a project token naming no UUID matched")
	}

	repo := Record{Name: "bitbucket-token--repo", Type: TypeRepo, Coverage: []string{"acme/app"}}
	if repo.MatchesProject("proj-uuid") {
		t.Error("a repository token matched a project UUID, and only a project token has one")
	}
}
