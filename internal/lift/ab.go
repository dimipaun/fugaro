package lift

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/task"
)

// PlanAB is B9's planner (AB3): a repository's ab.rate: 0 opt-out moves into
// the project layer's ab.repos.<owner/name>, where AB1 put it and ee/ab reads
// it (D3237, spec §5.3).
//
// It is a move and not a rename because ab.rate is lifted: release A stops
// honouring it in a repository's own file (S1 drops it in config.Resolve), so
// the opt-out has to reach the project before it is dropped — otherwise the
// repository's runs become eligible for A/B sampling at the project's rate,
// which is what the opt-out exists to prevent.
//
// What it moves, and what it does not:
//
//   - Only ab.rate. A repository may write nothing else under ab: and no rate
//     but 0 (validateRepoAB, D556, D557), so a repository that opted out is the
//     whole of this item's input, and the write is ab.repos.<repo>: 0. A file
//     whose block states no rate states nothing and is left as it is: the key
//     that lifts is ab.rate, not the block around it.
//   - A rate a repository may not write is named and left alone. Its file is
//     one the config loader refuses, so nothing of the block is moved rather
//     than an opt-out a person believes they wrote becoming a sampling rate in
//     the project.
//   - A repository the project already answers for in ab.repos keeps the
//     project's value: this item never overwrites a layer value, and names the
//     file's opt-out instead (C2).
//   - A repository with a migration record is named with C1's own sentence and
//     moved no further (D3305): update cannot commit to a repository, so
//     deleting ab.rate from its file is the person's.
//
// It acts only on a repository absent from Project.Migrated (D3305) and on one
// the reader could read at all, whose bytes are YAML (D3306). The write is a
// scalar at ab.repos.<repo>, the shape M2 lands with the layer editor's scalar
// write; a write that would leave ab.repos holding more repositories than
// validateAB accepts is named and left alone, because a layer the loader
// refuses is one every run of the project then fails to read — and the bound is
// read against the writes planned so far, since one update moves many
// repositories.
//
// It answers no error: a repository it cannot move is a line, so update
// converges for the rest (C2).
func PlanAB(p Project, repos []Repo) (Change, error) {
	held := projectABRepos(p.Layer)
	var out Change
	seen := map[string]bool{}
	for _, r := range sortedRepos(repos) {
		if r.Repo == "" || r.Unreadable != "" {
			continue
		}
		slug, err := task.Slug(r.Provider, r.Repo)
		if err != nil {
			out.Lines = append(out.Lines, leftAloneAB(r.Repo, err))
			continue
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		mine, err := repoABOf(r.Config)
		if err != nil {
			out.Lines = append(out.Lines, leftAloneAB(r.Repo, fmt.Errorf("fugaro.yaml cannot be read: %w", err)))
			continue
		}
		if !mine.rateSet {
			continue
		}
		if p.Migrated[slug] {
			out.Lines = append(out.Lines, stillWrittenAB(r.Repo, r.Config)...)
			continue
		}
		if mine.rate != 0 {
			out.Lines = append(out.Lines, fmt.Sprintf("%s: ab.rate %v is not a repository's to write, only 0 is (D557); nothing of its ab: block was moved", r.Repo, mine.rate))
			continue
		}
		if kept, ok := held[r.Repo]; ok {
			out.Lines = append(out.Lines, fmt.Sprintf("%s: ab.rate 0 dropped; the project's ab.repos.%s (%v) stands", r.Repo, r.Repo, kept))
			continue
		}
		if len(held) >= rigor.MaxRepoLevels {
			out.Lines = append(out.Lines, fmt.Sprintf("%s: ab.repos is full: %d of the %d repositories it may hold, counting the ones this run moves", r.Repo, len(held), rigor.MaxRepoLevels))
			continue
		}
		out.LayerEdits = append(out.LayerEdits, LayerEdit{
			Path:  []string{"ab", "repos", r.Repo},
			Value: "0",
		})
		out.Lines = append(out.Lines, fmt.Sprintf("%s: ab.rate 0 -> ab.repos.%s", r.Repo, r.Repo))
		held[r.Repo] = 0
		out.Records = append(out.Records, slug)
	}
	return out, nil
}

// repoAB is a repository's own ab: block as this item reads it.
type repoAB struct {
	// rateSet is whether the file states ab.rate at all, which is the key that
	// lifts: `ab:` and `ab: {}` state nothing this item moves.
	rateSet bool
	rate    float64
}

// repoABOf reads data's ab.rate. The walk to the one key is what tells a file
// that writes no block, or a block with no rate in it, from one that writes
// ab.rate: 0; decoding the document into config.Config would fail for the sake
// of blocks another migration item owns.
func repoABOf(data []byte) (repoAB, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return repoAB{}, err
	}
	node := documentValue(&doc, "ab")
	if node == nil || (node.Kind == yaml.ScalarNode && node.ShortTag() == "!!null") {
		return repoAB{}, nil
	}
	var block struct {
		Rate *float64 `yaml:"rate"`
	}
	if err := node.Decode(&block); err != nil {
		return repoAB{}, err
	}
	if block.Rate == nil {
		return repoAB{}, nil
	}
	return repoAB{rateSet: true, rate: *block.Rate}, nil
}

// projectABRepos is the layer's ab.repos as this item reads it: the repositories
// the project already answers for, by the value it states, so a line about one
// can print what stands. It is a copy, because this item adds the repositories
// it writes to it and the layer tree is M2's, not a planner's to write into.
func projectABRepos(layer map[string]any) map[string]any {
	block, _ := layerValue(layer, "ab", "repos").(map[string]any)
	out := make(map[string]any, len(block))
	for name, v := range block {
		out[name] = v
	}
	return out
}

// stillWrittenAB is the line for a repository already migrated (D3305) whose
// file still writes ab.rate: C1's own sentence, which names the key, its new
// home and the command that states it there.
func stillWrittenAB(repo string, data []byte) []string {
	var out []string
	for _, n := range config.LiftedKeyNotes(data) {
		if n.Path != "ab.rate" {
			continue
		}
		out = append(out, repo+": "+n.Why)
	}
	return out
}

// leftAloneAB is the line for a repository this item cannot move: what is wrong,
// and that nothing of it was touched, since release A stops reading ab.rate the
// moment it ships.
func leftAloneAB(repo string, err error) string {
	return fmt.Sprintf("%s: %v; nothing of its ab: block was moved", repo, err)
}
