package config

import (
	"slices"
	"strings"
	"testing"
)

// dead_keys_test.go is L13's own test (module `dead-keys`, D3299): the keys
// that are dead in a repository's own fugaro.yaml — the escalation ladder's two
// grade models, which D3237 moves to the project (C1, S1), and
// agent.compression, which no value but off survives (F16) — and the grade rows
// that stay somebody else's: model-credentials-routing's two `.access` rows,
// named and still honoured until that plan's own release 2 (R11).
//
// What the drop does is TestResolveDropsLiftedKeys' business, one case per
// lifted key; what these tests hold is the scope table beside it — which row
// may still say InRepo, and what the row a person reads says the drop is.

// TestRepoModelGradesModelDropped: a repository's own model_grades.<grade>.model
// is dropped before the merge and named where it was written, in both arms of
// Resolve, and its row says so — the drop is the lift's and names the key, not
// the merge recording a value (R2, R11). The row keeps InRepo on purpose:
// release B takes it off (XB1), and until then the key is still one a file may
// write, which is why the naming has a place to be read.
//
// Mutation (honour the repository's grade again, drop the naming, or write the
// merge back into the row): this test fails.
func TestRepoModelGradesModelDropped(t *testing.T) {
	const grades = "model_grades:\n  strong: {model: repo-9, access: pool-b}\n  strongest: {model: repo-10}\n"
	for _, arm := range []struct {
		what  string
		layer *ProjectLayer
		want  string
	}{
		{"without a layer", nil, "none"},
		{"with a layer", mustLayer(t, liftLayerHead+"defaults: {model_grades: {strong: {model: proj-5}}}\n"), "proj-5"},
	} {
		cfg, res, ps := Resolve([]byte(liftFileHead+grades+liftFileWorkflows), arm.layer)
		if len(ps) > 0 {
			t.Fatalf("%s: %v", arm.what, ps)
		}
		// Both grades' models went: the one the project states stands in its
		// place, and the one no layer states is no grade at all.
		if got := gradeModelRead(GradeStrong)(cfg); got != arm.want {
			t.Errorf("%s: the strong grade's model is %q, want %q", arm.what, got, arm.want)
		}
		if got := gradeModelRead(GradeStrongest)(cfg); got != "none" {
			t.Errorf("%s: the strongest grade's model is %q, want none: no layer states one", arm.what, got)
		}
		var named []string
		for _, n := range res.Lifted {
			named = append(named, n.Path)
		}
		if want := []string{"model_grades.strong.model", "model_grades.strongest.model"}; !slices.Equal(named, want) {
			t.Errorf("%s: the resolution names %v, want the two grade models, %v", arm.what, named, want)
		}
		// The retired access beside the model is not this plan's to drop (R1).
		if cfg.ModelGrades == nil {
			t.Fatalf("%s: the grades block went whole, and the retired access in it is not the drop's", arm.what)
		}
		if g := cfg.ModelGrades.At(GradeStrong); g == nil || g.Access != "pool-b" {
			t.Errorf("%s: the strong grade is %+v, want the repository's own access", arm.what, g)
		}
	}

	for _, key := range []string{"model_grades.strong.model", "model_grades.strongest.model"} {
		row, ok := ScopeOf(key)
		if !ok {
			t.Fatalf("%s has no Scopes row", key)
		}
		if row.In&InRepo == 0 {
			t.Errorf("%s: In = %v, and a repository's file may still write the key until release B takes the row off (XB1)", key, row.In)
		}
		if row.Retired != 0 {
			t.Errorf("%s is marked Retired %v: a lifted key is not a retired one (R1)", key, row.Retired)
		}
		if !strings.Contains(row.Why, "dropped and named") {
			t.Errorf("%s: the row says %q, want the drop and the naming of it — nobody reads a grade the file wrote, so what a person is owed is the sentence, not a merge that recorded a value", key, row.Why)
		}
	}
}

// TestModelGradesAccessStaysMCRs: the grade `.access` rows are
// model-credentials-routing's, retired in a repository's file with that plan's
// own sentence, and R11 leaves them out of this one — an access is not lifted,
// so the pair of a grade still resolves with the project's model and the
// repository's own credential.
//
// Mutation (lift an access row, retire it in the project layer too, or give it
// this plan's sentence): this test fails.
func TestModelGradesAccessStaysMCRs(t *testing.T) {
	for _, key := range []string{"model_grades.strong.access", "model_grades.strongest.access"} {
		row, ok := ScopeOf(key)
		if !ok {
			t.Fatalf("%s has no Scopes row", key)
		}
		if row.Retired != InRepo {
			t.Errorf("%s is Retired %v, want %v: the access is retired in a repository's file and honoured there (R11)", key, row.Retired, InRepo)
		}
		if row.RetiredWhy != whyRetiredGradeAccess {
			t.Errorf("%s: the retired sentence is model-credentials-routing's own (MG5), and this row now says %q", key, row.RetiredWhy)
		}
		if slices.Contains(LiftedKeyPaths(), key) {
			t.Errorf("%s is lifted, and R11 leaves the access rows to model-credentials-routing", key)
		}
	}
}

// TestCompressionLeavesARepositoryFile: agent.compression is refused at any
// value but the empty one it already defaults to (config.Validate), so no
// repository decides it and its row loses InRepo (F16) — the layer a profile is
// written in is out for its own reason, the bucket object agent.oauth_token
// already distrusts, and the override is what is left.
//
// Mutation (put InRepo back on the row, or retire the key instead): this test
// fails.
func TestCompressionLeavesARepositoryFile(t *testing.T) {
	row, ok := ScopeOf("agent.compression")
	if !ok {
		t.Fatal("agent.compression has no Scopes row")
	}
	if row.In != InOverride {
		t.Errorf("agent.compression: In = %v, want %v alone: a repository's file no longer sets a key whose only admitted value is the default (F16)", row.In, InOverride)
	}
	if row.Retired != 0 {
		t.Errorf("agent.compression is marked Retired %v: the key is refused where it is written, not named and honoured (D3063)", row.Retired)
	}
	if row.Why == "" {
		t.Error("agent.compression: the row says nothing, and the layers beside it are the whole of why a repository may not set it")
	}
}
