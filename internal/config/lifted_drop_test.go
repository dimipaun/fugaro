package config

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// liftFileHead and liftFileWorkflows are the file the tests below are built
// from: a repository's own fugaro.yaml that resolves with no project layer,
// and that anchors to testAnchor (acme/acme-fugaro) so the same text resolves
// over a layer too.
const liftFileHead = "version: 1\nproject: acme\ngcp_project: acme-fugaro\ngit: {provider: github}\n"

const liftFileWorkflows = "workflows:\n  server:\n    commands: {build: b, test: t}\n"

// liftLayerHead is the project layer a row's own block is appended to.
const liftLayerHead = "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"

// liftRow is one lifted key as a test row: the text a repository's file writes
// for it at its own path, the reader of the resolved config that answers for
// the key, the value a config that still honoured it would answer (the red
// direction this test pins), and the project layer's own block stating the key
// in its new home — "" for the two keys no reader of a resolution reads out of
// the layer yet, which is why those two rows' layer arm states nothing and
// asks only that the repository's value not survive.
type liftRow struct {
	key      string
	write    string
	read     func(*Config) string
	sentinel string
	layer    string
}

// liftRows is one row per lifted key, in LiftedKeys' own path order, so a key
// added to the list fails this test until a row reads it.
//
// Every `write` is the file's own value and no other layer's: the model name
// repo-9 is one nothing else in the installation states, `ab.rate: 0` is the
// only value a repository may write (validateRepoAB), `hotfix` is a level a
// layer below states differently, and the area's name is the row's own.
func liftRows() []liftRow {
	return []liftRow{
		{"ab.rate", "ab: {rate: 0}", abRateRead, "0", ""},
		{"agent.crafts.coder.model", "agent: {crafts: {coder: {model: repo-9}}}", craftModelRead(CraftCoder), "repo-9", "defaults: {agent: {crafts: {coder: {model: proj-5}}}}"},
		{"agent.crafts.merge-judge.model", "agent: {crafts: {merge-judge: {model: repo-9}}}", craftModelRead(CraftMergeJudge), "repo-9", "defaults: {agent: {crafts: {merge-judge: {model: proj-5}}}}"},
		{"agent.crafts.review.model", "agent: {crafts: {review: {model: repo-9}}}", craftModelRead(CraftReview), "repo-9", "defaults: {agent: {crafts: {review: {model: proj-5}}}}"},
		{"agent.crafts.security-review.model", "agent: {crafts: {security-review: {model: repo-9}}}", craftModelRead(CraftSecurityReview), "repo-9", "defaults: {agent: {crafts: {security-review: {model: proj-5}}}}"},
		{"agent.crafts.senior-review.model", "agent: {crafts: {senior-review: {model: repo-9}}}", craftModelRead(CraftSeniorReview), "repo-9", "defaults: {agent: {crafts: {senior-review: {model: proj-5}}}}"},
		{"agent.levels.l1.model", "agent: {levels: {l1: {model: repo-9}}}", levelModelRead("l1"), "repo-9", "defaults: {agent: {levels: {l1: {model: proj-5}}}}"},
		{"agent.levels.l2.model", "agent: {levels: {l2: {model: repo-9}}}", levelModelRead("l2"), "repo-9", "defaults: {agent: {levels: {l2: {model: proj-5}}}}"},
		{"agent.levels.l3.model", "agent: {levels: {l3: {model: repo-9}}}", levelModelRead("l3"), "repo-9", "defaults: {agent: {levels: {l3: {model: proj-5}}}}"},
		{"agent.model", "agent: {model: repo-9}", agentModelRead, "repo-9", "defaults: {agent: {model: proj-5}}"},
		{"agent.models.background", "agent: {models: {background: repo-9}}", roleModelRead(func(a Agent) string { return a.Models.Background }), "repo-9", "defaults: {agent: {models: {background: proj-5}}}"},
		{"agent.models.coder", "agent: {models: {coder: repo-9}}", roleModelRead(func(a Agent) string { return a.Models.Coder }), "repo-9", "defaults: {agent: {models: {coder: proj-5}}}"},
		{"agent.models.diagnose", "agent: {models: {diagnose: repo-9}}", roleModelRead(func(a Agent) string { return a.Models.Diagnose }), "repo-9", "defaults: {agent: {models: {diagnose: proj-5}}}"},
		{"agent.models.reviewer", "agent: {models: {reviewer: repo-9}}", roleModelRead(func(a Agent) string { return a.Models.Reviewer }), "repo-9", "defaults: {agent: {models: {reviewer: proj-5}}}"},
		{"agent.models.vision", "agent: {models: {vision: repo-9}}", roleModelRead(func(a Agent) string { return a.Models.Vision }), "repo-9", "defaults: {agent: {models: {vision: proj-5}}}"},
		{"model_grades.strong.model", "model_grades: {strong: {model: repo-9}}", gradeModelRead(GradeStrong), "repo-9", "defaults: {model_grades: {strong: {model: proj-5}}}"},
		{"model_grades.strongest.model", "model_grades: {strongest: {model: repo-9}}", gradeModelRead(GradeStrongest), "repo-9", "defaults: {model_grades: {strongest: {model: proj-5}}}"},
		{"rigor.areas", "rigor: {areas: [{name: repo-area, match: 'src/**', level: pro}]}", rigorAreaRead, "repo-area", "defaults: {rigor: {areas: [{name: proj-area, match: 'src/**', level: pro}]}}"},
		{"rigor.default", "rigor: {default: hotfix}", rigorLevelRead, "hotfix", "defaults: {rigor: {default: critical}}"},
	}
}

// TestResolveDropsLiftedKeys is S1's acceptance, one case per lifted key and in
// both arms of Resolve: the file's own value is dropped before the merge, so
// what the resolution carries is what a file that writes no lifted key at all
// carries — Fugaro's own value without a project layer, the project's with one
// — and the resolution names every key it dropped, with the one sentence a
// person reads.
//
// The baseline is the same file with the row's lifted key left out, resolved
// with the same layer, and the reader's own answer is what is compared: a row
// therefore proves the key reaches no reader, not merely that a field is zero.
// The whole config is compared too, where the row's own block is not rigor:
// its key's presence is itself part of the repository's file (RepoRigorSet,
// R2), so an emptied rigor: block resolves to a config that keeps saying the
// repository wrote one — by design, and the one difference this test allows.
func TestResolveDropsLiftedKeys(t *testing.T) {
	rows := liftRows()
	var wrote []string
	for _, r := range rows {
		wrote = append(wrote, r.key)
	}
	if want := keysOf(LiftedKeys); !slices.Equal(wrote, want) {
		t.Fatalf("the rows are %v, want one per lifted key, %v", wrote, want)
	}
	base := []byte(liftFileHead + liftFileWorkflows)
	baseCfg, baseRes, ps := Resolve(base, nil)
	if len(ps) > 0 {
		t.Fatalf("the file that writes no lifted key does not resolve: %v", ps)
	}
	if len(baseRes.Lifted) != 0 {
		t.Fatalf("a file that writes no lifted key resolves with %+v, want no note", baseRes.Lifted)
	}
	for _, tc := range rows {
		t.Run(tc.key, func(t *testing.T) {
			file := []byte(liftFileHead + tc.write + "\n" + liftFileWorkflows)
			// The reader has to be a reader of the key: a config that still
			// honours the lifted keys — decodeRepo, the strict decode, which is
			// what a Fugaro that has not shipped the drop reads — answers the
			// file's own value. A reader that answered anything else would make
			// the rest of this row prove nothing.
			c, ps := decodeRepo(file)
			if len(ps) > 0 {
				t.Fatalf("the file does not decode: %v", ps)
			}
			if got := tc.read(c); got != tc.sentinel {
				t.Fatalf("a config that honours the lifted keys reads %s as %q, want the file's own %q: this row's reader does not read the key", tc.key, got, tc.sentinel)
			}
			layer := mustLayer(t, liftLayerHead+tc.layer+"\n")
			baseWithLayer, _, ps := Resolve(base, layer)
			if len(ps) > 0 {
				t.Fatalf("the file that writes no lifted key does not resolve over the layer: %v", ps)
			}
			for _, arm := range []struct {
				what  string
				layer *ProjectLayer
				want  *Config
			}{
				{"without a layer", nil, baseCfg},
				{"with a layer", layer, baseWithLayer},
			} {
				cfg, res, ps := Resolve(file, arm.layer)
				if len(ps) > 0 {
					t.Fatalf("%s: %v", arm.what, ps)
				}
				want := tc.read(arm.want)
				if got := tc.read(cfg); got != want {
					t.Errorf("%s: %s reads %q, and a file that writes no lifted key reads %q: the repository's own value is dropped, never honoured", arm.what, tc.key, got, want)
				}
				if c == nil || !slices.Equal(res.Lifted, LiftedKeyNotes(file)) {
					t.Errorf("%s: the resolution's dropped keys are %+v, want the notes of what the file wrote (%+v)", arm.what, res.Lifted, LiftedKeyNotes(file))
				}
				if len(res.Lifted) != 1 || res.Lifted[0].Path != tc.key {
					t.Fatalf("%s: the resolution names %+v, want %s alone", arm.what, res.Lifted, tc.key)
				}
				if !strings.HasPrefix(tc.key, "rigor.") && cfg.SHA256() != arm.want.SHA256() {
					t.Errorf("%s: the resolved config is not the one the file without %s resolves to, key by key: %s", arm.what, tc.key, cfg.SHA256())
				}
			}
		})
	}
}

// TestResolveKeepsMCRRetiredKeys is the other half of R1: model-credentials-
// routing's retired keys — agent.auth, agent.oauth_token, a craft's access and
// a grade's access — are named and still honoured until that plan's own release
// 2, so the drop leaves every one of them exactly as the file wrote it, in the
// same blocks whose models it takes.
//
// The grade's half is read over a layer that states that grade's model, which
// is where the project keeps it now: a written grade names a model (D2039,
// ValidateModelGrades), so an entry the drop left with its access alone is
// refused by that rule wherever no layer states the model — the file's own
// model being the one the drop took. That refusal is the codebase's and not
// this drop's, and relaxing it is MCR's release 1 and DK1's business.
func TestResolveKeepsMCRRetiredKeys(t *testing.T) {
	const authAndCraft = "agent:\n  auth: cascade\n  oauth_token: work\n  crafts:\n    coder: {model: repo-9, access: pool-a}\n"
	const grades = "model_grades:\n  strong: {model: repo-9, access: pool-b}\n"
	for _, tc := range []struct {
		what    string
		file    string
		layer   *ProjectLayer
		dropped []string
	}{
		{"without a layer", authAndCraft, nil, []string{"agent.crafts.coder.model"}},
		{"with a layer", authAndCraft + grades,
			mustLayer(t, liftLayerHead+"defaults: {model_grades: {strong: {model: proj-5}}}\n"),
			[]string{"agent.crafts.coder.model", "model_grades.strong.model"}},
	} {
		file := []byte(liftFileHead + tc.file + liftFileWorkflows)
		cfg, res, ps := Resolve(file, tc.layer)
		if len(ps) > 0 {
			t.Fatalf("%s: %v", tc.what, ps)
		}
		if cfg.Agent.Auth != AuthCascade {
			t.Errorf("%s: agent.auth is %q, want the file's own %q: it is retired, not lifted", tc.what, cfg.Agent.Auth, AuthCascade)
		}
		if cfg.Agent.OAuthToken != "work" {
			t.Errorf("%s: agent.oauth_token is %q, want the file's own", tc.what, cfg.Agent.OAuthToken)
		}
		// The craft keeps the half that stays, and the model beside it went.
		if got := cfg.Agent.Crafts.Coder; got != (Craft{Access: "pool-a"}) {
			t.Errorf("%s: the coder craft is %+v, want the access alone: the access is retired, the model is lifted", tc.what, got)
		}
		// The grade keeps its access too, and takes the project's model where
		// the project states one: the two halves of a block stay apart (R1).
		if cfg.ModelGrades != nil {
			if got := cfg.ModelGrades.Strong; got == nil || got.Access != "pool-b" || got.Model != "proj-5" {
				t.Errorf("%s: the strong grade is %+v, want the project's model and the repository's access", tc.what, got)
			}
		} else if strings.Contains(tc.file, "model_grades") {
			t.Errorf("%s: the grades block was dropped whole, and MCR's access row is not this plan's to drop", tc.what)
		}
		var got []string
		for _, n := range res.Lifted {
			got = append(got, n.Path)
		}
		if !slices.Equal(got, tc.dropped) {
			t.Errorf("%s: the dropped keys are %v, want the models alone, %v", tc.what, got, tc.dropped)
		}
		// A retired key is in the resolution's sources, where it is written:
		// nothing about it was dropped.
		for _, path := range []string{"agent.crafts.coder.access", "agent.auth", "agent.oauth_token"} {
			if src := res.SourceOf(path); src != SourceRepo {
				t.Errorf("%s: %s has source %q, want %q", tc.what, path, src, SourceRepo)
			}
		}
	}
}

// TestRepoRigorFlagsSeeNoLiftedKey is R2's ordering: the fields computed from
// the file's own tree are computed after the drop, so a key the drop removed is
// in none of them — while the block the drop emptied still opts the repository
// into its own rigor rules, and the rigor: shorthand, which is not lifted, is
// still read.
func TestRepoRigorFlagsSeeNoLiftedKey(t *testing.T) {
	file := []byte(liftFileHead +
		"rigor:\n  default: hotfix\n  areas:\n    - {name: repo-area, match: 'src/**', level: pro}\n" +
		"agent:\n  levels:\n    l1: {model: none}\n" +
		liftFileWorkflows)
	for _, arm := range []struct {
		what  string
		layer *ProjectLayer
	}{
		{"without a layer", nil},
		{"with a layer", mustLayer(t, liftLayerHead)},
	} {
		cfg, res, ps := Resolve(file, arm.layer)
		if len(ps) > 0 {
			t.Fatalf("%s: %v", arm.what, ps)
		}
		if !cfg.RepoRigorSet {
			t.Errorf("%s: RepoRigorSet is false: the file writes rigor:, and an emptied block still opts in (R2)", arm.what)
		}
		if cfg.RepoRigor == nil || cfg.RepoRigor.Default != "" || len(cfg.RepoRigor.Areas) != 0 {
			t.Errorf("%s: RepoRigor = %+v, want a block carrying nothing: its own keys were lifted", arm.what, cfg.RepoRigor)
		}
		if cfg.RepoRigorLevelSet {
			t.Errorf("%s: RepoRigorLevelSet is true, and the rule writing level: went with rigor.areas", arm.what)
		}
		if cfg.RepoRigorScalarSet {
			t.Errorf("%s: RepoRigorScalarSet is true, and the file writes the block form", arm.what)
		}
		if cfg.RepoLevelsNoneModelSet {
			t.Errorf("%s: RepoLevelsNoneModelSet is true, and the model named none went with agent.levels.l1.model", arm.what)
		}
		if len(res.Lifted) != 3 {
			t.Errorf("%s: the resolution names %+v, want rigor.areas, rigor.default and agent.levels.l1.model", arm.what, res.Lifted)
		}
	}
	// The shorthand is not on the list: `rigor: critical` is the repository's
	// own level, written the one way that says nothing else (D20), and the drop
	// leaves it where it is.
	cfg, res, ps := Resolve([]byte(liftFileHead+"rigor: critical\n"+liftFileWorkflows), nil)
	if len(ps) > 0 {
		t.Fatalf("a file writing the rigor: shorthand does not resolve: %v", ps)
	}
	if !cfg.RepoRigorScalarSet || cfg.RepoRigor == nil || cfg.RepoRigor.Default != "critical" {
		t.Errorf("the rigor: shorthand reads %+v (scalar set %v), want the file's own level: it is not lifted", cfg.RepoRigor, cfg.RepoRigorScalarSet)
	}
	if len(res.Lifted) != 0 {
		t.Errorf("a file writing only the rigor: shorthand resolves with %+v, want no note", res.Lifted)
	}
}

// TestAgentOfDropsLiftedKeys is R21: AgentOf reads the repository's file
// without Resolve, and the block it hands back is the one a run would use — so
// it drops the lifted keys too, and what it answers is exactly what the same
// file with those keys taken out answers. The retired keys of the same blocks
// stay, as they do in a resolution.
func TestAgentOfDropsLiftedKeys(t *testing.T) {
	file := []byte("agent:\n  auth: cascade\n  model: repo-9\n  models: {coder: repo-9, reviewer: repo-9, vision: repo-9}\n" +
		"  crafts:\n    coder: {model: repo-9, access: pool-a}\n  levels:\n    l1: {model: repo-9, effort: high}\n" +
		liftFileHead + liftFileWorkflows)
	// The same file with every lifted key taken out, and nothing else changed:
	// the emptied models: block goes with its keys, the craft and the level keep
	// the halves that stay.
	without := []byte("agent:\n  auth: cascade\n" +
		"  crafts:\n    coder: {access: pool-a}\n  levels:\n    l1: {effort: high}\n" +
		liftFileHead + liftFileWorkflows)
	got, ok := AgentOf(file)
	if !ok {
		t.Fatal("AgentOf does not read the file")
	}
	want, ok := AgentOf(without)
	if !ok {
		t.Fatal("AgentOf does not read the file with the lifted keys taken out")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AgentOf = %+v, want %+v: the lifted keys are dropped, exactly as a file without them reads", got, want)
	}
	if got.Model != "" || got.Models.Coder != "" || got.Models.Reviewer != "" || got.Models.Vision != "" {
		t.Errorf("AgentOf hands back %+v, and every model of it is lifted", got)
	}
	if got.Crafts.Coder != (Craft{Access: "pool-a"}) {
		t.Errorf("the coder craft reads %+v, want the access alone: the model is lifted, the access is not", got.Crafts.Coder)
	}
	if got.Levels.L1 == nil || got.Levels.L1.Model != "" || got.Levels.L1.Effort != "high" {
		t.Errorf("l1 reads %+v, want the effort alone", got.Levels.L1)
	}
	if got.Auth != AuthCascade {
		t.Errorf("agent.auth reads %q, want the file's own %q: it is retired, not lifted", got.Auth, AuthCascade)
	}
	// A file whose agent block is nothing but lifted keys reads as one that
	// writes no agent block, auth excepted, which carries the default.
	only := []byte("agent: {model: repo-9}\n" + liftFileHead + liftFileWorkflows)
	gotOnly, ok := AgentOf(only)
	if !ok {
		t.Fatal("AgentOf does not read a file whose agent block is all lifted keys")
	}
	empty, ok := AgentOf([]byte(liftFileHead + liftFileWorkflows))
	if !ok {
		t.Fatal("AgentOf does not read a file with no agent block")
	}
	if !reflect.DeepEqual(gotOnly, empty) {
		t.Errorf("AgentOf reads %+v from a file whose agent block is all lifted keys, want %+v", gotOnly, empty)
	}
	// And bytes this Fugaro cannot read at all are still "cannot tell".
	if _, ok := AgentOf([]byte("agent: [not an agent block]\n")); ok {
		t.Error("AgentOf reads a file whose agent block is not a block")
	}
}

// abRateRead is ab.rate as its readers see it: the rate, and "none" for no
// block at all. Presence is what a repository's opt-out is made of (D556/D557),
// so nil and a block carrying 0 are different answers, and a reader that
// could not tell them apart could not see this key dropped either.
func abRateRead(c *Config) string {
	if c.AB == nil {
		return "none"
	}
	return strconv.FormatFloat(c.AB.Rate, 'f', -1, 64)
}

// craftModelRead is a craft's model, with nothing inherited.
func craftModelRead(name string) func(*Config) string {
	return func(c *Config) string { return c.Agent.Crafts.Of(name).Model }
}

// agentModelRead is agent.model, the model every role falls back to.
func agentModelRead(c *Config) string { return c.Agent.Model }

// roleModelRead is one of agent.models' role slots.
func roleModelRead(of func(Agent) string) func(*Config) string {
	return func(c *Config) string { return of(c.Agent) }
}

// levelModelRead is a review level's model, "none" for a level the file does
// not write — which an emptied level block also reads as, a level writing no
// model being a level on the model its own layer resolves.
func levelModelRead(name string) func(*Config) string {
	return func(c *Config) string {
		var l *LevelSetting
		switch name {
		case "l1":
			l = c.Agent.Levels.L1
		case "l2":
			l = c.Agent.Levels.L2
		case "l3":
			l = c.Agent.Levels.L3
		}
		if l == nil || l.Model == "" {
			return "none"
		}
		return l.Model
	}
}

// gradeModelRead is a grade's model, "none" for a grade the file does not
// write (or one left with no model).
func gradeModelRead(name string) func(*Config) string {
	return func(c *Config) string {
		if c.ModelGrades == nil {
			return "none"
		}
		var g *Craft
		switch name {
		case GradeStrong:
			g = c.ModelGrades.Strong
		case GradeStrongest:
			g = c.ModelGrades.Strongest
		}
		if g == nil || g.Model == "" {
			return "none"
		}
		return g.Model
	}
}

// rigorLevelRead is the level a repository's own rigor block names, "none" for
// a block that names none — which a block the drop emptied also reads as: the
// block's presence opts the repository into its own rules (R2), and an opt-in
// with no level of its own is not a level.
func rigorLevelRead(c *Config) string {
	if c.Rigor == nil || c.Rigor.Default == "" {
		return "none"
	}
	return string(c.Rigor.Default)
}

// rigorAreaRead is the name of the first of a repository's own area rules,
// "none" for no rule at all.
func rigorAreaRead(c *Config) string {
	if c.Rigor == nil || len(c.Rigor.Areas) == 0 {
		return "none"
	}
	return c.Rigor.Areas[0].Name
}
