package runner

import (
	"errors"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
)

// TestTheStubAccessorAnswersTheHandedEntryAndTheRule pins the runner's side of
// C1 (b) until CM8's accessor lands (I1 deletes the stub): the entry the
// launcher put in the execution's environment where the run carries one, and the
// migration's rule over the retired vocabulary where it does not.
//
// The half a run's safety rests on is the refusal: a document this release
// cannot read back is an error and never a fall back to the rule. It was written
// by the launcher of the same release, so a document half understood is one that
// could send a stage's calls to a credential the launcher never resolved, while
// the rule reads a file the launcher may already have replaced.
func TestTheStubAccessorAnswersTheHandedEntryAndTheRule(t *testing.T) {
	in := craft.LegacyInput{
		Auth:        config.AuthOAuth,
		OAuthToken:  "alice",
		Credentials: []config.Credential{{Name: "alice", Type: config.CredentialTypeClaudeOAuth}},
	}
	coder, ok := craft.LegacyAccess(in, "implement", "")
	if !ok {
		t.Fatal("the rule answered nothing for an oauth repository with a token")
	}
	reviewer, ok := craft.LegacyAccess(in, "review", "")
	if !ok {
		t.Fatal("the rule answered nothing for the review stage")
	}
	handed, err := craft.Resolved{
		Version: craft.ResolvedVersion,
		Crafts:  map[string]craft.Access{config.CraftCoder: coder},
		Grades:  map[string]craft.Access{config.GradeStrong: reviewer},
	}.Encode()
	if err != nil {
		t.Fatalf("encoding the handed entry: %v", err)
	}

	t.Run("the entry answers the stage's craft", func(t *testing.T) {
		got, err := stubAccess(handed, craft.LegacyInput{}, "implement", "")
		if err != nil || got.Name != coder.Name {
			t.Fatalf("stubAccess(implement) = %+v, err=%v; want the coder's entry %+v", got, err, coder)
		}
	})

	t.Run("a task that names a grade is answered the grade's entry where the entry holds one", func(t *testing.T) {
		got, err := stubAccess(handed, craft.LegacyInput{}, "review", config.GradeStrong)
		if err != nil || got.Name != reviewer.Name {
			t.Fatalf("stubAccess(review, strong) = %+v, err=%v; want the grade's entry %+v", got, err, reviewer)
		}
	})

	t.Run("a craft the entry does not hold is refused, never served another's", func(t *testing.T) {
		if got, err := stubAccess(handed, craft.LegacyInput{}, "adversarial", ""); !errors.Is(err, errStubAccess) {
			t.Fatalf("stubAccess(adversarial) = %+v, err=%v; want the stub's refusal", got, err)
		}
	})

	t.Run("a stage the run's vocabulary has no craft for is refused", func(t *testing.T) {
		if _, err := stubAccess(handed, craft.LegacyInput{}, "not_a_stage", ""); !errors.Is(err, errStubAccess) {
			t.Fatalf("stubAccess(not_a_stage) err=%v, want the stub's refusal", err)
		}
	})

	t.Run("no entry at all is the migration's rule", func(t *testing.T) {
		got, err := stubAccess("", in, "implement", "")
		if err != nil || got.Name != coder.Name {
			t.Fatalf("stubAccess(no entry, implement) = %+v, err=%v; want the rule's answer %+v", got, err, coder)
		}
		if _, err := stubAccess("", craft.LegacyInput{}, "implement", ""); !errors.Is(err, errStubAccess) {
			t.Fatalf("a run with no entry and no retired key: err=%v, want the stub's refusal", err)
		}
	})

	t.Run("an entry this release cannot read is an error and never the rule", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			doc  string
			want error
		}{
			{"another version", `{"version":99}`, craft.ErrResolvedVersion},
			{"not a document", "{not json", craft.ErrResolvedMalformed},
		} {
			got, err := stubAccess(tc.doc, in, "implement", "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("%s: stubAccess err=%v, want %v", tc.name, err, tc.want)
			}
			if got.Name != "" {
				t.Fatalf("%s: the stub answered %+v beside the error, want nothing", tc.name, got)
			}
		}
	})
}
