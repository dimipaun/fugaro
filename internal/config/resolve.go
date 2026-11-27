package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/policy"
)

// Sources of a resolved value (Resolution.SourceOf).
const (
	SourceDefault = "default"
	SourceProject = "project"
	SourceRepo    = "repo"
)

// SourceProfile is the source of a value a profile set.
func SourceProfile(name string) string { return "profile " + name }

// IsLayerSource is whether src names the project layer rather than the
// repository's own file: SourceProject, or a SourceProfile of one of the
// layer's profiles. SourceDefault and SourceRepo are not.
func IsLayerSource(src string) bool {
	return src == SourceProject || strings.HasPrefix(src, "profile ")
}

// CodeNeedsLayer is the Code of a problem that only a project layer can
// solve: a profile named, or no workflows, with no layer given.
const CodeNeedsLayer = "needs_project_layer"

// Resolution says how a config was resolved.
type Resolution struct {
	// Sources maps each key a layer set (a fugaro.yaml path, with real
	// workflow names; a list is one key) to its source: SourceProject,
	// SourceRepo or SourceProfile(name). A key in no layer is a default.
	Sources map[string]string
	// LayerSHA256 is the project layer's sha256, "" without one.
	LayerSHA256 string
	// ConfigSHA256 is the resolved config's (Config.SHA256).
	ConfigSHA256 string
	// Lifted is every lifted key the repository's file wrote, in path order,
	// each with the sentence a person reads (config.LiftedKeys): the keys
	// release A drops and names rather than refuses. The drop is in every
	// resolution, both arms and every ref, so this is where a caller finds what
	// the repository wrote and a run no longer honours (S2, S5); a key here is
	// in no Sources entry, because a dropped key is set by no layer.
	Lifted []RetiredKeyNote
	// Ignored is every tighten-only value (B4, spec §5.2) the repository's
	// file wrote that did not tighten the project's, in path order, in the
	// shape policy.Effective.Ignored uses: Key is the path, Value the text the
	// repository wrote, Effective the project's, and Source is always
	// SourceProject — the layer the value that stands comes from. The dropped
	// key is in Sources with that same source, so a reader can tell "the
	// project's value stands" from "nothing set it" (S2 prints these).
	Ignored []policy.Ignored
	// Unchecked is every tighten-only key the repository's file wrote where
	// the project layer sets no value, in path order: the repository's value
	// stands unjudged, and the places that apply the run's mode take the
	// tighter of it and the mode's own (R3, RT2). The arm with no project
	// layer marks every one of them.
	Unchecked []string
}

// listIndexRE is a list index in a path (cache[0].key).
var listIndexRE = regexp.MustCompile(`\[\d+\]`)

// SourceOf is the source of path: its own, else its nearest listed
// ancestor's, else SourceDefault. A list is one key, so list indexes are
// ignored: cache[0].key is cache's.
func (r *Resolution) SourceOf(path string) string {
	for p := listIndexRE.ReplaceAllString(path, ""); p != ""; {
		if s, ok := r.Sources[p]; ok {
			return s
		}
		i := strings.LastIndex(p, ".")
		if i < 0 {
			break
		}
		p = p[:i]
	}
	return SourceDefault
}

// SHA256 is the hex sha256 of the config's JSON encoding: the same config
// gives the same sum in the CLI, the runner, Cloud Build and the check job.
func (c *Config) SHA256() string {
	data, err := json.Marshal(c)
	if err != nil {
		panic(err) // a Config always encodes
	}
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// Resolve is a repository's fugaro.yaml resolved over the project layer l
// and Fugaro's defaults (docs/layered-config/spec.md §4): for every key,
// the repository's value, else its workflow's profile's, else the project
// layer's defaults', else Fugaro's default. Maps merge key by key; a
// scalar or a list is replaced whole; null is "not set here". A nil l is
// Parse as before 0.6.0, except that naming a profile, or a key YAML reads
// as other than a string (1:, true:, null:), is an error.
//
// The merge works on YAML nodes, never on decoded values: a value reaches
// the resolved config as the text and tag it was written with, so 1.10 stays
// "1.10" and 010 stays "010", exactly as Parse reads them.
func Resolve(data []byte, l *ProjectLayer) (*Config, *Resolution, []Problem) {
	repo, ps := decodeRepo(data)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, yamlProblems(err)
	}
	tree, ps := docTree(&doc)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	// The lifted keys go before anything reads the tree, in both arms: the
	// merge, the repository's own flags and the config a run carries see a
	// file without them, and the notes of what was dropped travel in the
	// resolution (R2, contract-lift-keys.md). The drop takes a copy: the file
	// itself is read as written and stays read as written — release A reads a
	// lifted key and does not honour it, so a repository is never refused for
	// one until release B (spec §4) — and both arms judge the problems below on
	// the file as its author wrote it, while what they hand back comes from the
	// file the drop left.
	droppedTree, lifted := dropLiftedKeys(tree)
	dropped := repo
	if len(lifted) > 0 {
		if dropped, ps = decodeTree(droppedTree); len(ps) > 0 {
			return nil, nil, ps
		}
	}
	if l == nil {
		if ps := layerlessProblems(repo); len(ps) > 0 {
			return nil, nil, ps
		}
		applyDefaults(repo)
		if ps := Validate(repo); len(ps) > 0 {
			return nil, nil, noLayerHint(repo, ps)
		}
		out := dropped
		applyDefaults(out)
		setRepoOwn(out, dropped, droppedTree, tree)
		res := &Resolution{Sources: map[string]string{}, Lifted: lifted, Unchecked: uncheckedIn(droppedTree)}
		markLeaves(droppedTree, "", SourceRepo, res.Sources)
		res.ConfigSHA256 = out.SHA256()
		return out, res, nil
	}
	if ps := anchorProblems(repo, l); len(ps) > 0 {
		return nil, nil, ps
	}
	res := &Resolution{Sources: map[string]string{}, LayerSHA256: l.SHA256, Lifted: lifted}
	m := &mergeState{sources: res.Sources}
	merged, profileOf, ps := layerMerge(tree, repo, l, m)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	c, ps := decodeTree(merged)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	c.Layer = l
	// The repository's own rigor: block, kept beside the merged one: the
	// project layer's area rules stay in effect next to the repository's
	// rather than being replaced by them (D20c), so a consumer that resolves
	// a mode needs both (Config.RigorConfig). Validation judges the file as
	// written, so the block standing here is the one the file wrote; the
	// config handed back carries the block the drop left (R2).
	c.RepoRigor = repo.Rigor
	applyDefaults(c)
	if ps := Validate(c); len(ps) > 0 {
		return nil, nil, annotate(ps, res, profileOf)
	}
	res.Ignored, res.Unchecked = m.record()
	out := c
	if len(lifted) > 0 {
		// The config a run carries: the same merge over the file the drop
		// left, whose sources are this config's — a dropped key is in no
		// Sources entry, and no reader downstream sees a lifted value. Its
		// tighten record is this merge's too, since a lifted key is one the
		// repository's file wrote and the carried config does not honour.
		m = &mergeState{sources: map[string]string{}}
		droppedMerged, _, ps := layerMerge(droppedTree, dropped, l, m)
		if len(ps) > 0 {
			return nil, nil, ps
		}
		if out, ps = decodeTree(droppedMerged); len(ps) > 0 {
			return nil, nil, ps
		}
		out.Layer = l
		res.Sources = m.sources
		res.Ignored, res.Unchecked = m.record()
	}
	setRepoOwn(out, dropped, droppedTree, tree)
	applyDefaults(out)
	tightenFocus(out, tree, layerDefaults(l))
	res.ConfigSHA256 = out.SHA256()
	return out, res, nil
}

// layerDefaults is the project layer's defaults block, nil without one: the
// tree the focus merge reads a layer's own text from.
func layerDefaults(l *ProjectLayer) map[string]any {
	d, _ := l.tree["defaults"].(map[string]any)
	return d
}

// layerMerge is Resolve's layer arm's merge: the project layer's defaults with
// tree over them, the repository's own ab: repos gone and the workflows
// resolved. The arm takes it twice where the file wrote a lifted key — once
// over the file as written, whose problems are the ones judged, and once over
// the file the drop left, which is the config a run carries — and tree and the
// decode the workflows read are the two calls' only difference: they are one
// file, as written and as the drop left it.
func layerMerge(tree map[string]any, repo *Config, l *ProjectLayer, m *mergeState) (map[string]any, map[string]string, []Problem) {
	merged := map[string]any{}
	if d, ok := l.tree["defaults"].(map[string]any); ok {
		overlay(merged, d, "", SourceProject, m)
	}
	top := maps1(tree)
	delete(top, "workflows")
	overlay(merged, top, "", SourceRepo, m)
	dropLayerRigorRepos(merged, m.sources)
	wfs, profileOf, ps := resolveWorkflows(tree, repo, l, m)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	merged["workflows"] = wfs
	return merged, profileOf, nil
}

// setRepoOwn fills the fields a resolved config carries about the repository's
// own file: the rigor block kept beside the merged one, and the flags that
// answer what that file writes. The four a lifted key decides — the rigor
// block, its two spellings and its areas, and a review level's `none` literal,
// which is agent.levels.lN.model and so a lifted key too — are read from the file the
// drop left, because a run resolves no lifted value and the gates that read
// them judge the configuration a run would (R2); every other flag answers its
// own question about the text as its author wrote it, which the lift leaves
// alone. dropped is that file decoded (own) and as a tree, written the file as
// written.
func setRepoOwn(c *Config, own *Config, dropped, written map[string]any) {
	c.RepoRigor = own.Rigor
	c.RepoRigorSet = setsRigor(dropped)
	c.RepoRigorLevelSet = setsRigorLevel(dropped)
	c.RepoRigorScalarSet = setsRigorScalar(dropped)
	c.RepoLevelsNoneModelSet = setsLevelsNoneModel(dropped)
	c.RepoGovernanceSet = setsGovernance(written)
	c.RepoGitAuthorSet = setsGitAuthor(written)
	c.RepoSecuritySet = setsSecurity(written)
	c.RepoPlanningSet = setsPlanning(written)
	c.RepoStopFractionSet = setsStopFraction(written)
	c.RepoWorkspaceBeatSet = setsWorkspaceBeat(written)
	c.RepoTriageSet = setsTriage(written)
	c.RepoReviewAgainstTaskSet = setsReviewAgainstTask(written)
	c.RepoVisionSet = setsVision(written)
	c.RepoMergeJudgeCraftSet = setsMergeJudgeCraft(written)
	c.RepoWorkflowABSet = setsWorkflowAB(written)
}

// layerlessProblems are the keys that need a project layer when there is
// none.
func layerlessProblems(c *Config) []Problem {
	why := "profiles come from the project layer (" + LayerKey + " in the runs bucket of gcp_project), and none applies"
	if c.GCPProject == "" {
		why = "profiles come from the project layer, which applies only to a fugaro.yaml with gcp_project:"
	}
	var ps []Problem
	if c.Profile != "" {
		ps = append(ps, Problem{Path: "profile", Message: fmt.Sprintf("names profile %q, but %s", c.Profile, why), Code: CodeNeedsLayer})
	}
	for _, name := range sortedKeys(c.Workflows) {
		if p := c.Workflows[name].Profile; p != "" {
			ps = append(ps, Problem{Path: "workflows." + showKey(name) + ".profile", Message: fmt.Sprintf("names profile %q, but %s", p, why), Code: CodeNeedsLayer})
		}
	}
	return ps
}

// noLayerHint adds, to the no-workflows problem of an anchored file, that
// a project layer could supply them.
func noLayerHint(c *Config, ps []Problem) []Problem {
	if c.GCPProject == "" {
		return ps
	}
	for i, p := range ps {
		if p.Path == "workflows" {
			ps[i].Message += fmt.Sprintf("; a fugaro.yaml without workflows takes one from project %s's layer, and none was found (fugaro config layer)", showKey(c.Project))
			ps[i].Code = CodeNeedsLayer
		}
	}
	return ps
}

func anchorProblems(c *Config, l *ProjectLayer) []Problem {
	var ps []Problem
	if c.Project != l.Project {
		ps = append(ps, Problem{Path: "project", Message: fmt.Sprintf("is %q, but the project layer given is project %q's", c.Project, l.Project)})
	}
	if c.GCPProject != l.GCPProject {
		ps = append(ps, Problem{Path: "gcp_project", Message: fmt.Sprintf("is %q, but the project layer given is GCP project %q's; the project layer applies only to a fugaro.yaml whose gcp_project: names it", c.GCPProject, l.GCPProject)})
	}
	return ps
}

// resolveWorkflows builds the merged workflows: and says which profile
// each workflow took ("" for none).
func resolveWorkflows(tree map[string]any, repo *Config, l *ProjectLayer, m *mergeState) (map[string]any, map[string]string, []Problem) {
	profiles, _ := l.tree["profiles"].(map[string]any)
	names := strings.Join(sortedKeys(profiles), ", ")
	profileTree := func(name string) (map[string]any, bool) {
		p, ok := profiles[name].(map[string]any)
		if !ok {
			return nil, false
		}
		q := maps1(p) // overlay copies the maps it writes into
		delete(q, "description")
		return q, true
	}
	out, profileOf := map[string]any{}, map[string]string{}
	wm, _ := tree["workflows"].(map[string]any)
	if len(wm) == 0 {
		name, from := repo.Profile, SourceRepo
		if name == "" {
			name, from = l.DefaultProfile, SourceProject
		}
		if name == "" {
			return nil, nil, []Problem{{Path: "workflows", Message: fmt.Sprintf("must define at least one workflow, or name a profile with profile: (project %s's layer has no default_profile; its profiles: %s)", l.Project, names)}}
		}
		p, ok := profileTree(name)
		if !ok {
			return nil, nil, []Problem{{Path: "profile", Message: fmt.Sprintf("names profile %q, which project %s's layer does not have (its profiles: %s)", name, l.Project, names)}}
		}
		path := "workflows." + ImplicitWorkflow
		w := map[string]any{}
		overlay(w, p, path, SourceProfile(name), m)
		w["profile"] = name
		m.sources[path+".profile"] = from
		out[ImplicitWorkflow], profileOf[ImplicitWorkflow] = w, name
		return out, profileOf, nil
	}
	if repo.Profile != "" {
		return nil, nil, []Problem{{Path: "profile", Message: "applies only to a fugaro.yaml with no workflows:; name each workflow's profile with workflows.<name>.profile"}}
	}
	var ps []Problem
	for _, name := range sortedKeys(wm) {
		rw, _ := wm[name].(map[string]any)
		// path matches Validate's own Problem.Path (validate.go uses
		// showKey(name) too), so annotate's lookups into src and profileOf
		// by path still find a workflow name with a control or bidi
		// character in it.
		path := "workflows." + showKey(name)
		w := map[string]any{}
		if pname := repo.Workflows[name].Profile; pname != "" {
			p, ok := profileTree(pname)
			if !ok {
				ps = append(ps, Problem{Path: path + ".profile", Message: fmt.Sprintf("names profile %q, which project %s's layer does not have (its profiles: %s)", pname, l.Project, names)})
				continue
			}
			// The inline escape hatch: a repository Dockerfile replaces the
			// profile's generated-image settings. dockerfile: "" names no
			// Dockerfile, so it leaves them.
			if repo.Workflows[name].Dockerfile != "" {
				delete(p, "image")
			}
			overlay(w, p, path, SourceProfile(pname), m)
			profileOf[showKey(name)] = pname
		}
		overlay(w, rw, path, SourceRepo, m)
		out[name] = w
	}
	return out, profileOf, ps
}

// overlay merges src into dst at path: maps key by key, anything else
// replaced whole; a null in src sets nothing. m records the leaves src set,
// and what the tighten rule (B4) did with the ones the repository's layer
// wrote. A review level is the one block whose merge is not always key by
// key, and mergeLevel says why.
func overlay(dst, src map[string]any, path string, source string, m *mergeState) {
	for _, k := range sortedKeys(src) {
		v := src[k]
		if v == nil {
			continue
		}
		p := k
		if path != "" {
			p = path + "." + k
		}
		if sm, ok := v.(map[string]any); ok {
			// Both layers decode into the same typed Config, so a key is a
			// map in both or a map in neither.
			dm, set := dst[k].(map[string]any)
			if !set {
				dm = map[string]any{}
				dst[k] = dm
			}
			if isLevelPath(p) {
				mergeLevel(dm, sm, p, source, m, set)
				continue
			}
			overlay(dm, sm, p, source, m)
			continue
		}
		if !m.tightenLeaf(dst, k, v, p, source) {
			continue
		}
		dst[k] = v // a leaf is a node, which nothing changes
		m.sources[p] = source
	}
}

// isLevelPath reports whether path is one review level's own block
// ("agent.levels.l1"), the only node overlay merges specially.
func isLevelPath(path string) bool {
	level, ok := strings.CutPrefix(path, "agent.levels.")
	return ok && (level == "l1" || level == "l2" || level == "l3")
}

// mergeLevel merges the review-level block sm into dm, the level at path. It
// is not the key-by-key merge of every other block, because a level that is
// off has no other keys: LevelSetting.validate refuses `none: true` beside
// one, in a single file, and the literal model name none — the same switch
// written the other way (NoneModel) — beside one too. A layer saying the level
// does not run, in either spelling, is therefore saying the level does not
// run, and the model, effort and focus the layer below gives it are dropped
// rather than merged into a level that would carry both and be refused for it;
// a layer writing any other key is turning the level back on, so an inherited
// switch is cleared instead of merged into the level it contradicts. Both
// directions are one rule — the switch is the level's own, not one of its
// settings — and they are what lets a repository turn on a level the project
// turned off, and turn off one the project left to the layers below it.
//
// B4 narrows that rule for the repository's layer (spec §5.2): a level on
// beats off, so the repository's off is kept only where the project's block
// sets no level at all — where it is the repository's own answer, marked
// unchecked (R3) — and dropped where the project turns the level on, recorded
// like any other value that did not tighten. projectSet is whether the project
// layer wrote this level's block.
func mergeLevel(dm, sm map[string]any, path string, source string, m *mergeState, projectSet bool) {
	off, wrote := levelNone(sm)
	if levelModelNone(sm) {
		off, wrote = true, true
	}
	if off && source == SourceRepo {
		switch {
		case !projectSet:
			m.unchecked = append(m.unchecked, path+".none")
		case levelTreeOff(dm):
			// The project says the level does not run and so does the
			// repository: the repository adds nothing, so nothing is dropped.
			sm = withoutKeys(sm, "none", "model")
			off, wrote = false, false
		default:
			m.ignored = append(m.ignored, policy.Ignored{Key: path + ".none", Value: "true", Effective: "false", Source: SourceProject})
			sm = withoutKeys(sm, "none", "model")
			off, wrote = false, false
		}
	}
	switch {
	case off:
		clear(dm)
		dropSources(m.sources, path)
	case wrote || writesAKey(sm):
		delete(dm, "none")
		delete(m.sources, path+".none")
		if levelModelNone(dm) {
			delete(dm, "model")
			delete(m.sources, path+".model")
		}
	}
	overlay(dm, sm, path, source, m)
}

// levelTreeOff is whether a level block says the level does not run, in
// either spelling: `none: true`, or its model named none (NoneModel).
func levelTreeOff(m map[string]any) bool {
	off, _ := levelNone(m)
	return off || levelModelNone(m)
}

// levelNone reads a level block's `none`: whether it is true, and whether the
// block writes the key at all. The value is a *yaml.Node (nodeTree), never a
// decoded Go value, and Decode is what reads it, so a node the merged config
// itself would refuse to decode as a bool is not read as one here either; a
// null is not written, and sets nothing, as it does everywhere in the merge.
func levelNone(m map[string]any) (off, wrote bool) {
	n, ok := m["none"].(*yaml.Node)
	if !ok {
		return false, false
	}
	var b bool
	return n.Decode(&b) == nil && b, true
}

// levelModelNone is whether a level block's model is the literal none, which
// is the level's own switch written the other way (NoneModel). The node is
// read the way levelNone reads the `none` key, so a value the config would
// refuse to decode as a string is not read as one here either.
func levelModelNone(m map[string]any) bool {
	n, ok := m["model"].(*yaml.Node)
	if !ok {
		return false
	}
	var s string
	return n.Decode(&s) == nil && s == NoneModel
}

// writesAKey is whether a level block sets one of the level's own keys, which
// is what turns an inherited none: true back on.
func writesAKey(m map[string]any) bool {
	for k, v := range m {
		if k != "none" && v != nil {
			return true
		}
	}
	return false
}

// dropSources forgets the keys under path, which a replaced level no longer
// has: a source left behind would name a layer for a key the resolved config
// does not carry.
func dropSources(sources map[string]string, path string) {
	prefix := path + "."
	for p := range sources {
		if strings.HasPrefix(p, prefix) {
			delete(sources, p)
		}
	}
}

// dropLayerRigorRepos removes defaults.rigor.repos from the document the merged
// config is decoded from. The map is the project layer's alone (LD4): it names
// the level each repository of the project runs at, and a repository's own file
// cannot write it — Rigor.UnmarshalYAML refuses a key that is not default or
// areas — so the key has no repository half to merge with and, left in the
// merged block, the strict decode below refuses it ("field repos not found in
// type config.Rigor") and every repository of the project stops resolving. The
// layer keeps the map, and ProjectRigor reads it there.
func dropLayerRigorRepos(merged map[string]any, sources map[string]string) {
	r, ok := merged["rigor"].(map[string]any)
	if !ok {
		return
	}
	if _, ok := r["repos"]; !ok {
		return
	}
	delete(r, "repos")
	delete(sources, "rigor.repos")
	dropSources(sources, "rigor.repos")
}

// markLeaves records source for every leaf of v under path.
func markLeaves(v any, path, source string, sources map[string]string) {
	if m, ok := v.(map[string]any); ok {
		for k, e := range m {
			p := k
			if path != "" {
				p = path + "." + k
			}
			markLeaves(e, p, source, sources)
		}
		return
	}
	if v != nil && path != "" {
		sources[path] = source
	}
}

// setsRigor is ProjectLayer.SetsRigor for a repository file's own tree: whether
// the text writes the rigor: key, at any value. It reads the tree and not the
// resolved value for the reason that method states — a runner older than
// rigorSince decodes the file with KnownFields, so the key alone is what it
// refuses, and an explicit null, which decodes to no block at all, is refused
// exactly like a full one. Its answer becomes Config.RepoRigorSet.
func setsRigor(tree map[string]any) bool {
	_, ok := tree["rigor"]
	return ok
}

// setsRigorLevel is setsRigor for the `level:` key of a rigor area rule:
// whether the text writes one, at any value. It reads the tree for that
// method's reason and one more of its own — rigor.Area.UnmarshalYAML folds
// `level:` and `mode:` into the rule's level, so the parsed rules cannot say
// which spelling was written — and a runner older than rigorSpellingSince
// refuses `level:` as a field it does not know, in a file it decodes with
// KnownFields. Its answer becomes Config.RepoRigorLevelSet.
func setsRigorLevel(tree map[string]any) bool {
	return rigorAreaLevelWritten(tree["rigor"])
}

// setsRigorScalar is setsRigor for the rigor: key written as a scalar — the
// shorthand `rigor: critical`, which Rigor.UnmarshalYAML reads as a block
// carrying only default. The two forms decode to the same block, so the parsed
// value cannot tell them apart, and a runner older than rigorSpellingSince
// refuses the scalar outright ("cannot unmarshal !!str into config.Rigor")
// where it reads the block. Its answer becomes Config.RepoRigorScalarSet.
func setsRigorScalar(tree map[string]any) bool {
	n, ok := tree["rigor"].(*yaml.Node)
	return ok && n.ShortTag() == "!!str"
}

// rigorAreaLevelWritten is whether a decoded rigor: value writes the `level:`
// key of one of its area rules: the shared walk behind setsRigorLevel and
// ProjectLayer.SetsRigorLevel, which read it out of a repository's tree and a
// layer's respectively.
func rigorAreaLevelWritten(node any) bool {
	return rigorAreaKeyWritten(node, "level")
}

// rigorMembersWritten is whether a decoded rigor: value writes what LD4 adds
// to the project layer's rigor block: a `members:` key of one of its area
// rules, or the per-repository `repos:` map beside them. It is what
// ProjectLayer.SetsRigorMembers answers, and both keys are the project
// layer's alone — a repository's own file knows no repos: and refuses members:
// on a rule of its own (rigor.Area.Validate) — so a runner older than
// rigorMembersSince (internal/cli) refuses either as something its KnownFields
// decoder does not know, in the layer it decodes at bootstrap, for every
// repository of the project at once.
func rigorMembersWritten(node any) bool {
	m, ok := node.(map[string]any)
	if !ok {
		return false
	}
	if _, ok := m["repos"]; ok {
		return true
	}
	return rigorAreaKeyWritten(node, "members")
}

// rigorAreaKeyWritten is whether a decoded rigor: value writes key as a key of
// one of its area rules, at any value. The rules are a mapping's value in this
// tree only when the whole rigor: block is one, so the list arrives as the node
// Resolve keeps it as (nodeTree's answer for a sequence) and is walked as
// nodes.
func rigorAreaKeyWritten(node any, key string) bool {
	m, ok := node.(map[string]any)
	if !ok {
		return false
	}
	areas, ok := m["areas"].(*yaml.Node)
	if !ok || areas.Kind != yaml.SequenceNode {
		return false
	}
	for _, rule := range areas.Content {
		if rule.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(rule.Content); i += 2 {
			if rule.Content[i].Value == key {
				return true
			}
		}
	}
	return false
}

// setsGovernance is ProjectLayer.SetsGovernance for a repository file's own
// tree: whether the text writes the governance: key, at any value, which is
// the same question setsRigor answers for rigor:, for the same reason. Its
// answer becomes Config.RepoGovernanceSet.
func setsGovernance(tree map[string]any) bool {
	_, ok := tree["governance"]
	return ok
}

// setsGitAuthor is setsRigor for the git.author key: whether the repository's
// text writes git.author, at any value. It reads the tree and not the resolved
// value for the same reason — git.author is a value rather than a block, so an
// explicit null decodes to GitAuthorFugaro and is indistinguishable from the
// key being absent — and the gate needs the key, because a runner older than
// gitAuthorSince decodes the file with KnownFields and refuses git.author
// whatever it says. Its answer becomes Config.RepoGitAuthorSet.
func setsGitAuthor(tree map[string]any) bool {
	git, ok := tree["git"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = git["author"]
	return ok
}

// setsStopFraction is setsGitAuthor for the agent.review_stop_fraction key:
// whether the repository's text writes it, at any value. It reads the tree and
// not the resolved Agent.ReviewStopFraction for the same reason — the key
// written 0 resolves to the default the key's absence resolves to — and the
// gate needs the key, because a runner older than stopFractionSince
// (internal/cli) decodes the file with KnownFields and refuses
// agent.review_stop_fraction whatever it says. Its answer becomes
// Config.RepoStopFractionSet.
func setsStopFraction(tree map[string]any) bool {
	agent, ok := tree["agent"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = agent["review_stop_fraction"]
	return ok
}

// setsWorkspaceBeat is setsStopFraction for the agent.workspace_beat key:
// whether the repository's text writes it, at any value. It reads the tree and
// not the resolved Agent.WorkspaceBeat for the same reason — the key written
// true, the default, resolves to the same answer as the key's absence — and
// the gate needs the key, because a runner older than workspaceBeatSince
// (internal/cli) decodes the file with KnownFields and refuses
// agent.workspace_beat whatever it says. Its answer becomes
// Config.RepoWorkspaceBeatSet.
func setsWorkspaceBeat(tree map[string]any) bool {
	agent, ok := tree["agent"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = agent["workspace_beat"]
	return ok
}

// setsTriage is setsStopFraction for the agent.triage key: whether the
// repository's text writes it, at any value. It reads the tree and not the
// resolved Agent.Triage for the reason the fields beside it read the tree — a
// runner older than triageSince (internal/cli) decodes the file with
// KnownFields and refuses agent.triage whatever it says, so the key's presence
// is the question the image-skew gate needs, and the resolved pointer answers
// a different one (what the switch says). Its answer becomes
// Config.RepoTriageSet.
//
// The project layer's half of this key does not exist: agent.triage is not a
// layer key at all (config.Scopes gives it InRepo alone), so there is no
// ProjectLayer.SetsTriage beside SetsStopFraction. The owner's published node
// is the other home, and it is carried into the run as a ceiling
// (policy.Layer.Triage) rather than through this tree.
func setsTriage(tree map[string]any) bool {
	agent, ok := tree["agent"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = agent["triage"]
	return ok
}

// setsReviewAgainstTask is setsTriage for the agent.review_against_task key:
// whether the repository's text writes it, at any value. It reads the tree and
// not the resolved Agent.ReviewAgainstTask for the reason the fields beside it
// read the tree — a runner older than reviewSpecSince (internal/cli) decodes
// the file with KnownFields and refuses agent.review_against_task whatever it
// says, so the key's presence is the question the image-skew gate needs, and
// the resolved pointer answers a different one (whether the check runs). Its
// answer becomes Config.RepoReviewAgainstTaskSet.
//
// The project layer's half of this key does not exist: agent.review_against_task
// is not a layer key at all (config.Scopes gives it InRepo alone), so there is
// no ProjectLayer.SetsReviewAgainstTask beside SetsStopFraction. The owner's
// published node is the other home, and it is carried into the run as a ceiling
// (policy.Layer.ReviewAgainstTask) rather than through this tree.
func setsReviewAgainstTask(tree map[string]any) bool {
	agent, ok := tree["agent"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = agent["review_against_task"]
	return ok
}

// setsSecurity is setsRigor for the security: block: whether the repository's
// text writes the key, at any value. It reads the tree and not the resolved
// block for the same reason — a runner older than securitySince decodes the
// file with KnownFields, so the key alone is what it refuses, and an explicit
// null, which decodes to no block at all, is refused exactly like a full one.
// Its answer becomes Config.RepoSecuritySet.
//
// It is the repository's half: the project layer's security: block (D1501) is
// read from the layer itself (ProjectLayer.Security) and never merges into this
// tree, which is what keeps a repository's own block from replacing the
// project's rows — the two meet only in the union, project first (D1506). The
// image-skew gates in internal/cli judge both halves, the way rigorSince's
// carry both of rigor's: RepoSecuritySet for this tree, ProjectLayer's
// SetsSecurity for the layer, because a job image older than the release that
// reads the key decodes either file strictly and refuses it at bootstrap.
func setsSecurity(tree map[string]any) bool {
	_, ok := tree["security"]
	return ok
}

// setsLevelsNoneModel is ProjectLayer.SetsLevelsNoneModel for a repository
// file's own tree: whether the text names a level's model with the literal
// none. It reads the tree and not the resolved agent.levels for the reason
// setsRigor states — LevelSetting.validate folds the literal into the level's
// switch, so the resolved block reads the same as one that wrote `none: true`
// and cannot say which spelling the file used, while the gate needs it: a
// runner older than noneModelSince reads the literal as a model ID. Its answer
// becomes Config.RepoLevelsNoneModelSet.
func setsLevelsNoneModel(tree map[string]any) bool {
	return levelsNoneModel(tree["agent"])
}

// levelsNoneModel reports whether an agent block names a level's model with
// the literal none, which both trees — the repository file's and the project
// layer's defaults — read the same way: the level's own mapping, with a model
// leaf that decodes to NoneModel. A level the block writes as something other
// than a mapping is refused by the block's own decode, so it is not one.
func levelsNoneModel(agent any) bool {
	a, ok := agent.(map[string]any)
	if !ok {
		return false
	}
	levels, ok := a["levels"].(map[string]any)
	if !ok {
		return false
	}
	for _, lv := range levels {
		if m, ok := lv.(map[string]any); ok && levelModelNone(m) {
			return true
		}
	}
	return false
}

// setsPlanning is ProjectLayer.SetsPlanning for a repository file's own tree:
// whether the text writes the planning: key, at any value, which is the same
// question setsGovernance answers for governance:, for the same reason. Its
// answer becomes Config.RepoPlanningSet.
func setsPlanning(tree map[string]any) bool {
	_, ok := tree["planning"]
	return ok
}

// setsVision is ProjectLayer.SetsVision for a repository file's own tree:
// whether the text writes agent.models.vision, at any value, which is the same
// walk both trees take for the key — agent.models.vision is InProject | InRepo
// (config.Scopes), so either file may write it and either may be the one the
// older runner's decoder refuses. Its answer becomes Config.RepoVisionSet.
func setsVision(tree map[string]any) bool {
	return visionKey(tree["agent"])
}

// setsMergeJudgeCraft is ProjectLayer.SetsMergeJudgeCraft for a repository
// file's own tree: whether the text writes agent.crafts.merge-judge, at any
// value, which is the same walk both trees take for the key — agent.crafts is
// InProject | InRepo (config.Scopes), so either file may write it and either
// may be the one the older runner's decoder refuses. Its answer becomes
// Config.RepoMergeJudgeCraftSet.
func setsMergeJudgeCraft(tree map[string]any) bool {
	return mergeJudgeCraftKey(tree["agent"])
}

// mergeJudgeCraftKey reports whether an agent block writes crafts.merge-judge,
// the walk both trees read the same way: the key's own presence in the
// mapping, whatever it holds, and false when the block or the crafts mapping
// is written as something other than a mapping (which the block's own decode
// refuses anyway). A null leaf and an empty block count, which is why this is
// not a value read: `crafts: { merge-judge: }` and `merge-judge: {}` are the
// same line to a runner older than mergeJudgeSince as a block naming a model
// and an access. The other four crafts are not asked about: their keys are
// 0.24.0's and an older runner's decoder knows them.
func mergeJudgeCraftKey(agent any) bool {
	a, ok := agent.(map[string]any)
	if !ok {
		return false
	}
	c, ok := a["crafts"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = c[CraftMergeJudge]
	return ok
}

// visionKey reports whether an agent block writes models.vision, the walk both
// trees read the same way: the key's own presence in the mapping, whatever it
// holds, and false when the block or the models mapping is written as something
// other than a mapping (which the block's own decode refuses anyway). A null
// leaf counts, which is why this is not a value read: `models: { vision: }` is
// the same line to a runner older than visionSince as a model ID.
func visionKey(agent any) bool {
	a, ok := agent.(map[string]any)
	if !ok {
		return false
	}
	m, ok := a["models"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = m["vision"]
	return ok
}

// setsWorkflowAB is setsVision for a workflow's own ab: key: the workflows of
// the repository file's text that write one, at any value. It reads the tree
// and not the resolved block for the reason setsSecurity reads it — a runner
// older than workflowABSince (internal/cli) decodes the file with KnownFields
// and refuses the key whatever it says, so a null leaf, which decodes to no
// block at all, is exactly as fatal to that runner as a block naming a level,
// and the resolved pointer answers the other question (which arm the file asks
// for). Its answer becomes Config.RepoWorkflowABSet, and the names are sorted
// so the same file always answers the same list.
//
// The walk is the repository file's own, and only its own: workflows.<name>.ab
// is InRepo (config.Scopes), so a project layer that wrote the key is refused
// by the layer's own loader before any image is judged, and a profile cannot
// carry it into a resolution at all.
func setsWorkflowAB(tree map[string]any) []string {
	wfs, ok := tree["workflows"].(map[string]any)
	if !ok {
		return nil
	}
	names := map[string]bool{}
	for name, wf := range wfs {
		// A workflow written as something other than a mapping is refused by
		// its own decode, so it writes no ab: key here either.
		m, ok := wf.(map[string]any)
		if !ok {
			continue
		}
		if _, wrote := m["ab"]; wrote {
			names[name] = true
		}
	}
	return sortedKeys(names)
}

// docTree is a YAML document as Resolve merges it (nodeTree).
func docTree(doc *yaml.Node) (map[string]any, []Problem) {
	if len(doc.Content) == 0 {
		return map[string]any{}, nil
	}
	t, p := nodeTree("", doc.Content[0])
	if p != nil {
		return nil, []Problem{*p}
	}
	m, _ := t.(map[string]any)
	return m, nil
}

// nodeTree is n as Resolve merges it: a mapping as a map[string]any, with
// aliases and merge keys expanded; null as nil; any other value as a copy
// of its own *yaml.Node, so marshalling writes back the text and tag it was
// written with. A key YAML reads as other than a string is refused: it
// would name a different key once marshalled back.
func nodeTree(path string, n *yaml.Node) (any, *Problem) {
	n = deref(n)
	switch {
	case n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null":
		return nil, nil
	case n.Kind != yaml.MappingNode:
		return plainNode(n), nil
	}
	m := map[string]any{}
	var merges []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := deref(n.Content[i]), n.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" {
			merges = append(merges, deref(v))
			continue
		}
		if k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str" {
			at := path
			if at == "" {
				at = "the top level"
			}
			return nil, &Problem{Line: k.Line, Message: fmt.Sprintf("has, under %s, the key %q, which YAML reads as %s, not a string; quote it", at, showKey(k.Value), showKey(strings.TrimPrefix(k.ShortTag(), "!!")))}
		}
		p := showKey(k.Value)
		if path != "" {
			p = path + "." + p
		}
		t, prob := nodeTree(p, v)
		if prob != nil {
			return nil, prob
		}
		m[k.Value] = t
	}
	// A merged key never replaces one the mapping sets itself, and the
	// first of a list of merged mappings wins.
	for _, mv := range merges {
		from := []*yaml.Node{mv}
		if mv.Kind == yaml.SequenceNode {
			from = mv.Content
		}
		for _, f := range from {
			t, prob := nodeTree(path, f)
			if prob != nil {
				return nil, prob
			}
			sm, ok := t.(map[string]any)
			if !ok {
				return nil, &Problem{Line: f.Line, Message: "merges (<<) a value that is not a mapping"}
			}
			for k, v := range sm {
				if _, set := m[k]; !set {
					m[k] = v
				}
			}
		}
	}
	return m, nil
}

// deref follows an alias to the node it names.
func deref(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// plainNode is a copy of n with its aliases expanded and its anchors and
// comments dropped, so it marshals on its own. A folded or literal scalar's
// Value already holds the decoded string (Parse's), so re-emitting it in
// block style re-folds or re-indents that text as if it were still source,
// which can decode to a different string (the chomping indicator and any
// more-indented line are not recoverable from the decoded value alone);
// double-quoting re-emits Value byte for byte, so it replaces both. The tag
// stays explicit (e.g. !!binary) so the value still decodes the same way.
func plainNode(n *yaml.Node) *yaml.Node {
	n = deref(n)
	c := *n
	c.Anchor, c.HeadComment, c.LineComment, c.FootComment = "", "", "", ""
	if c.Kind == yaml.ScalarNode && c.Style&(yaml.FoldedStyle|yaml.LiteralStyle) != 0 {
		c.Style = yaml.DoubleQuotedStyle
		if n.Style&yaml.TaggedStyle != 0 {
			c.Style |= yaml.TaggedStyle
		}
	}
	if n.Content != nil {
		c.Content = make([]*yaml.Node, len(n.Content))
		for i, e := range n.Content {
			c.Content[i] = plainNode(e)
		}
	}
	return &c
}

// annotate says, on each problem of a resolved config, which layer set the
// value, or that neither the repository nor the workflow's profile did.
func annotate(ps []Problem, r *Resolution, profileOf map[string]string) []Problem {
	for i, p := range ps {
		if p.Path == "" {
			continue
		}
		switch src := r.SourceOf(p.Path); {
		case src != SourceDefault && src != SourceRepo:
			ps[i].Message += " (set by " + src + ")"
		case src == SourceDefault:
			parts := strings.SplitN(p.Path, ".", 3)
			if len(parts) >= 2 && parts[0] == "workflows" && profileOf[parts[1]] != "" {
				ps[i].Message += fmt.Sprintf(" (set neither by the repository nor by profile %s)", profileOf[parts[1]])
			}
		}
	}
	return ps
}
