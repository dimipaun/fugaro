package config

import (
	"strings"
	"testing"
)

// TestBitbucketTokenRecordForms: R1 pairs a token's type with the coverage
// form that states it, so a record is one of three shapes and nothing else —
// the coverage of a repository token is one repository (workspace/name), of a
// workspace token a whole workspace (workspace/*), of a project token the
// workspace slug alone beside the project's UUID or key — and the type and
// the form are two statements of one fact. The forms that agree are accepted
// and every way of disagreeing is refused, which is the whole of what this
// check is for: the type is what the add-time probe asks (T7) and the
// coverage is what the selector matches (T4), so a record whose two halves
// say different things would be probed as one type and selected as another.
func TestBitbucketTokenRecordForms(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record BitbucketToken
		msg    string // "" when the record is a form; else a sentence the refusal must hold
	}{
		{"repository", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeRepo, Coverage: []string{"acme/api"}}, ""},
		{"repository family", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeRepo, Coverage: []string{"acme/api-*"}}, ""},
		{"workspace", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeWorkspace, Coverage: []string{"acme/*"}}, ""},
		{"project by uuid", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeProject, Coverage: []string{"acme"}, ProjectUUID: "{b7f1c2d0}"}, ""},
		{"project by key", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeProject, Coverage: []string{"acme"}, ProjectKey: "API"}, ""},
		{"project by uuid and key", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeProject, Coverage: []string{"acme"}, ProjectUUID: "{b7f1c2d0}", ProjectKey: "API"}, ""},
		{"repository coverage typed workspace", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeWorkspace, Coverage: []string{"acme/api"}}, "repo"},
		{"workspace coverage typed repository", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeRepo, Coverage: []string{"acme/*"}}, "workspace"},
		{"workspace coverage typed project", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeProject, Coverage: []string{"acme/*"}, ProjectUUID: "{b7f1c2d0}"}, "workspace"},
		{"project coverage typed workspace", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeWorkspace, Coverage: []string{"acme"}}, "project"},
		{"project coverage typed repository", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeRepo, Coverage: []string{"acme"}}, "project"},
		{"no coverage", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeRepo}, "coverage is required"},
		{"no type", BitbucketToken{Name: "ci", Coverage: []string{"acme/api"}}, "type must be one of"},
		{"unknown type", BitbucketToken{Name: "ci", Type: "application", Coverage: []string{"acme/api"}}, "type must be one of"},
		{"project with no project", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeProject, Coverage: []string{"acme"}}, "project"},
		{"repository naming a project", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeRepo, Coverage: []string{"acme/api"}, ProjectUUID: "{b7f1c2d0}"}, "only a project token"},
		{"workspace naming a project key", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeWorkspace, Coverage: []string{"acme/*"}, ProjectKey: "API"}, "only a project token"},
		{"project covering two workspaces", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeProject, Coverage: []string{"acme", "other"}, ProjectUUID: "{b7f1c2d0}"}, "one project"},
		{"bare wildcard coverage", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeWorkspace, Coverage: []string{"*"}}, "coverage"},
		{"wildcard owner coverage", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeRepo, Coverage: []string{"*/api"}}, "coverage"},
		{"three-segment coverage", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeRepo, Coverage: []string{"acme/group/api"}}, "coverage"},
		{"project coverage a dot segment", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeProject, Coverage: []string{"."}, ProjectUUID: "{b7f1c2d0}"}, "coverage entry"},
		{"project coverage a parent segment", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeProject, Coverage: []string{".."}, ProjectUUID: "{b7f1c2d0}"}, "coverage entry"},
		{"repository coverage a parent segment", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeRepo, Coverage: []string{"acme/.."}}, "coverage entry"},
		{"empty coverage entry", BitbucketToken{Name: "ci", Type: BitbucketTokenTypeRepo, Coverage: []string{""}}, "empty"},
		{"no name", BitbucketToken{Type: BitbucketTokenTypeRepo, Coverage: []string{"acme/api"}}, "name is required"},
		{"name with a doubled dash", BitbucketToken{Name: "bitbucket-token--ci", Type: BitbucketTokenTypeRepo, Coverage: []string{"acme/api"}}, "must match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := ValidateBitbucketTokens([]BitbucketToken{tc.record})
			if tc.msg == "" {
				if len(ps) != 0 {
					t.Fatalf("a %s record was refused: %+v", tc.name, ps)
				}
				return
			}
			if len(ps) == 0 {
				t.Fatalf("a %s record was accepted", tc.name)
			}
			joined := ""
			for _, p := range ps {
				joined += p.Path + ": " + p.Message + "\n"
			}
			if !strings.Contains(joined, tc.msg) {
				t.Errorf("the refusal does not say %q:\n%s", tc.msg, joined)
			}
			if tc.record.Name != "" && !strings.Contains(joined, "bitbucket_tokens."+tc.record.Name) {
				t.Errorf("the refusal does not name the record:\n%s", joined)
			}
		})
	}
}

// TestBitbucketTokenRecordRefusesTypeMismatch is the red step's own case, on
// its own: a record typed `repo` whose coverage is `ws/*` — a workspace
// token's form — is refused, and so is its mirror. The refusal names the
// record and both halves, because the operator's repair is one of the two and
// the message cannot know which they meant.
func TestBitbucketTokenRecordRefusesTypeMismatch(t *testing.T) {
	for _, tc := range []struct{ typ, coverage string }{
		{BitbucketTokenTypeRepo, "myworkspace/*"},
		{BitbucketTokenTypeWorkspace, "myworkspace/api"},
	} {
		tok := BitbucketToken{Name: "ci", Type: tc.typ, Coverage: []string{tc.coverage}}
		ps := ValidateBitbucketTokens([]BitbucketToken{tok})
		if len(ps) != 1 {
			t.Fatalf("type %s with coverage %q = %+v, want one problem", tc.typ, tc.coverage, ps)
		}
		if ps[0].Path != "bitbucket_tokens.ci.coverage" {
			t.Errorf("the refusal is at %q, want the record's coverage", ps[0].Path)
		}
		if !strings.Contains(ps[0].Message, tc.coverage) || !strings.Contains(ps[0].Message, tc.typ) {
			t.Errorf("the refusal %q does not name both the coverage and the type", ps[0].Message)
		}
	}
	// The mirror is a form: the same coverage under the type it states is
	// accepted, so this is the pair that disagrees and not the coverage alone.
	if ps := ValidateBitbucketTokens([]BitbucketToken{{Name: "ci", Type: BitbucketTokenTypeWorkspace, Coverage: []string{"myworkspace/*"}}}); len(ps) != 0 {
		t.Fatalf("the workspace form was refused: %+v", ps)
	}
}

// TestBitbucketTokenRecordRefusesADuplicateName: a name is the token's
// identity — the bundle key bitbucket-token--<name> (T1) and every audit line
// (R5) are built from it — so two records cannot share one, and a block that
// did would have one entry of the bundle speaking for two records.
func TestBitbucketTokenRecordRefusesADuplicateName(t *testing.T) {
	ps := ValidateBitbucketTokens([]BitbucketToken{
		{Name: "ci", Type: BitbucketTokenTypeRepo, Coverage: []string{"acme/api"}},
		{Name: "ci", Type: BitbucketTokenTypeWorkspace, Coverage: []string{"acme/*"}},
	})
	if len(ps) != 1 {
		t.Fatalf("a duplicate name = %+v, want one problem", ps)
	}
	if !strings.Contains(ps[0].Message, "twice") {
		t.Errorf("the refusal %q does not say the name is defined twice", ps[0].Message)
	}
}
