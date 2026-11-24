package lift

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/task"
)

// PlanRigor is RG2: the migration item that moves a repository's own
// rigor.default and rigor.areas into the project layer, where D3294's order
// reads them.
//
// It is a move and not a rename because both keys are lifted (D3237, B11):
// release A stops honouring them in a repository's own file, so what a
// repository states has to reach the project before it is dropped. What each
// key becomes, and why:
//
//   - rigor.default — and the scalar shorthand `rigor: poc`, which says the
//     same thing (D20) — becomes defaults.rigor.repos.<owner/name>: the
//     project's own answer for one repository of it, which D3294 ranks above a
//     project-wide area and below a rule naming the repository in a member. A
//     project that already states an entry keeps it: this item never
//     overwrites a layer value an admin has written since, and it names the
//     file's value instead (C2, D3305).
//   - each rigor.areas rule becomes a project area of one member,
//     {repo, paths: [its match]}, keeping its name. A name the project layer
//     already holds cannot be kept: Select dedupes by name, so two rules under
//     one name would decide as one, silently dropping a level. R4's fallback
//     appends -<owner-repo>, cuts to rigor.MaxAreaName, and takes a short hash
//     when the cut collides too.
//   - an empty rigor: block sets no level and is still the repository's own
//     opt-in (Config.Configured, XB3). It becomes a rigor.repos entry at the
//     level it resolves to today, so retiring the block in release B does not
//     drop the opt-in that files this repository's backlog.
//
// A moved rule does not have to resolve to what it resolved to before, and
// that is what the item's lines have to say: D3294 reads a project area naming
// this repository above the project's entry for it and above the repository's
// own file, where the file outranked it before (LF4), while the file's own
// keys go away. R5 requires every repository and path whose level moves,
// either way, to be named with both levels, and update prints every line
// before it writes anything.
//
// It acts only on a repository absent from Project.Migrated (D3305) and on one
// the reader could read at all (D3306). A repository already migrated is named
// — the keys its file still writes, with C1's own sentence, which is the fix a
// person applies by hand, since update never commits to a repository. A
// repository whose block cannot be read, or whose block the project layer
// could not hold, is named and left alone entirely: half-migrating it would
// drop whatever was left behind the moment release A stops reading the key.
//
// The error is for an input it cannot read at all: the project layer's rigor
// block, which every repository's answer above depends on.
func PlanRigor(p Project, repos []Repo) (Change, error) {
	project, err := projectRigor(p.Layer)
	if err != nil {
		return Change{}, fmt.Errorf("the project layer's defaults.rigor cannot be read: %w", err)
	}
	taken := make(map[string]bool, len(project.Areas))
	for _, a := range project.Areas {
		taken[a.Name] = true
	}
	size := layerSize{areas: len(project.Areas), repos: len(project.Repos)}
	var out Change
	seen := map[string]bool{}
	for _, r := range sortedRepos(repos) {
		if r.Repo == "" || r.Unreadable != "" {
			continue
		}
		slug, err := task.Slug(r.Provider, r.Repo)
		if err != nil {
			out.Lines = append(out.Lines, leftAlone(r.Repo, err))
			continue
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		mine, err := repoRigorOf(r.Config)
		if err != nil {
			out.Lines = append(out.Lines, leftAlone(r.Repo, fmt.Errorf("fugaro.yaml cannot be read: %w", err)))
			continue
		}
		if !mine.block {
			continue
		}
		if p.Migrated[slug] {
			out.Lines = append(out.Lines, stillWritten(r.Repo, r.Config)...)
			continue
		}
		if msg := notMovable(project, mine, r.Repo, size); msg != "" {
			out.Lines = append(out.Lines, leftAlone(r.Repo, fmt.Errorf("%s", msg)))
			continue
		}
		out = moveRepoRigor(out, project, mine, r.Repo, slug, taken, &size)
	}
	return out, nil
}

// repoRigor is a repository's own rigor: block (D20) as its file writes it.
type repoRigor struct {
	// block is whether the file writes the key at all, even as a block that
	// sets nothing: the block is the repository's opt-in, which Configured
	// reads and this item has to carry into the project.
	block bool
	def   rigor.Rigor
	areas []rigor.Area
}

// repoRigorOf reads data's rigor: block. It is the reader a document's own
// decoder is (config.Rigor's own UnmarshalYAML, reached through the node so a
// block this cannot read is an error here rather than a key silently ignored),
// with the two empty spellings taken before it: `rigor:` and `rigor: {}` set
// nothing, and decoding either as the block would be a "rigor must be one of
// …" refusal for a file the config loader accepts.
func repoRigorOf(data []byte) (repoRigor, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return repoRigor{}, err
	}
	node := documentValue(&doc, "rigor")
	if node == nil {
		return repoRigor{}, nil
	}
	if node.Kind == yaml.ScalarNode && node.ShortTag() == "!!null" {
		return repoRigor{block: true}, nil
	}
	var r config.Rigor
	if err := node.Decode(&r); err != nil {
		return repoRigor{}, err
	}
	return repoRigor{block: true, def: r.Default, areas: r.Areas}, nil
}

// documentValue is the value of key in a document's top-level mapping, nil
// when the document sets no such key. It walks the tree rather than decoding
// into config.Config, because this reader is asking about one key: a file the
// other blocks of which are for another migration item is not this item's to
// refuse.
func documentValue(doc *yaml.Node, key string) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}

// projectRigor is the project layer's defaults.rigor as config.LayerRigor
// reads it, so a rule and a level here are the same values the layer file's
// own validation and the run's resolution use.
//
// The tree is the decoded layer file (docs/layered-config), so the block is a
// map[string]any and its sequences are *yaml.Node (nodeTree's shape); going
// back through YAML is what lets one decoder — config.LayerRigor with
// rigor.Area's own UnmarshalYAML, which refuses an unknown key — read the
// block from either shape.
func projectRigor(layer map[string]any) (config.LayerRigor, error) {
	block, _ := layerValue(layer, "defaults", "rigor").(map[string]any)
	if block == nil {
		return config.LayerRigor{}, nil
	}
	data, err := yaml.Marshal(block)
	if err != nil {
		return config.LayerRigor{}, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var out config.LayerRigor
	if err := dec.Decode(&out); err != nil {
		return config.LayerRigor{}, err
	}
	return out, nil
}

// layerValue walks a layer tree by key path, nil where the path runs out:
// through a key the tree does not set, or past a value that is not a mapping.
func layerValue(tree map[string]any, path ...string) any {
	var cur any = tree
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[key]
	}
	return cur
}

// notMovable is the one reason this item cannot move a repository's whole
// block, "" when it can. It answers for the whole block rather than for one
// key of it: release A drops the block's keys together, so moving the part
// this item can read would lose the part it cannot.
func notMovable(project config.LayerRigor, mine repoRigor, repo string, size layerSize) string {
	if mine.def != "" && !rigor.Known(mine.def) {
		return fmt.Sprintf("rigor.default %q is not one of %s", mine.def, rigor.RigorList())
	}
	for i, a := range mine.areas {
		if msg := a.Validate(); msg != "" {
			return fmt.Sprintf("rigor.areas[%d]: %s", i, msg)
		}
	}
	if n := size.areas + len(mine.areas); n > rigor.MaxAreas {
		return fmt.Sprintf("its %d areas would leave the project layer with %d, more than the %d it may hold", len(mine.areas), n, rigor.MaxAreas)
	}
	// Both writes below add a defaults.rigor.repos key — the default branch, and
	// the empty block, which becomes the level it resolves to today — so either
	// has to leave the map room: validateRigorRepos refuses the layer one entry
	// over the bound, and a published layer every run then fails to load is
	// worse than a repository this item names and leaves alone.
	if (mine.def != "" || len(mine.areas) == 0) && !hasRepoLevel(project, repo) && size.repos >= rigor.MaxRepoLevels {
		return fmt.Sprintf("defaults.rigor.repos is full: %d of the %d repositories it may hold, counting the ones this run moves", size.repos, rigor.MaxRepoLevels)
	}
	return ""
}

// layerSize is what the project layer's defaults.rigor holds, as this item
// spends it: the rules and the repository levels the layer will carry once the
// writes planned so far have landed. The guards read this and not the layer as
// it stood before the migration, because one update moves many repositories:
// the bound is the loader's own (config.validateRigor, validateRigorRepos), and
// a layer the loader refuses is one every run of the project then fails to
// read, so the last repository planned must see what the first one wrote.
type layerSize struct {
	areas int
	repos int
}

// hasRepoLevel reports whether the project layer answers for repo's whole
// repository, read exactly as Resolve reads it: a level of the vocabulary.
func hasRepoLevel(project config.LayerRigor, repo string) bool {
	lvl, ok := project.Repos[repo]
	return ok && rigor.Known(lvl)
}

// moveRepoRigor appends one repository's move to out: the layer writes, the
// lines a person reads, and — where it wrote anything — the slug the migration
// record is written under once those writes have landed (D3305).
func moveRepoRigor(out Change, project config.LayerRigor, mine repoRigor, repo, slug string, taken map[string]bool, size *layerSize) Change {
	before := rigor.Config{
		Project:      project.Default,
		ProjectAreas: project.Areas,
		ProjectRepos: project.Repos,
		Repo:         mine.def,
		RepoAreas:    mine.areas,
		RepoBlock:    true,
	}
	// What the layers resolve once release A drops the file's keys and the
	// project holds what this item writes: the repository's own keys gone, the
	// moved areas in the project's list, a moved default in the project's map.
	after := before
	after.Repo, after.RepoAreas = "", nil
	after.ProjectAreas = slices.Clone(project.Areas)
	after.ProjectRepos = maps.Clone(project.Repos)
	if after.ProjectRepos == nil {
		after.ProjectRepos = map[string]rigor.Rigor{}
	}
	kept, hasEntry := project.Repos[repo]
	wrote := false
	switch {
	case mine.def != "" && hasEntry:
		out.Lines = append(out.Lines, fmt.Sprintf("%s: rigor.default %s dropped; the project's defaults.rigor.repos.%s (%s) stands", repo, mine.def, repo, kept))
	case mine.def != "":
		out.LayerEdits = append(out.LayerEdits, LayerEdit{
			Path:  []string{"defaults", "rigor", "repos", repo},
			Value: string(mine.def),
		})
		out.Lines = append(out.Lines, fmt.Sprintf("%s: rigor.default %s -> defaults.rigor.repos.%s", repo, mine.def, repo))
		after.ProjectRepos[repo] = mine.def
		size.repos++
		wrote = true
	case len(mine.areas) == 0 && hasEntry:
		out.Lines = append(out.Lines, fmt.Sprintf("%s: an empty rigor: block; the project's defaults.rigor.repos.%s (%s) stands", repo, repo, kept))
	case len(mine.areas) == 0:
		lvl := rigor.Resolve(before, rigor.Request{Repo: repo}).Level
		out.LayerEdits = append(out.LayerEdits, LayerEdit{
			Path:  []string{"defaults", "rigor", "repos", repo},
			Value: string(lvl),
		})
		out.Lines = append(out.Lines, fmt.Sprintf("%s: an empty rigor: block -> defaults.rigor.repos.%s (%s), the level it resolves to today", repo, repo, lvl))
		after.ProjectRepos[repo] = lvl
		size.repos++
		wrote = true
	}
	for _, a := range mine.areas {
		name := areaName(a.Name, ownerRepoOf(slug), taken)
		taken[name] = true
		member := LayerItem{
			KeyField: "repo",
			Key:      repo,
			Lists:    map[string][]string{"paths": {a.Match}},
		}
		out.LayerEdits = append(out.LayerEdits, LayerEdit{
			Path: []string{"defaults", "rigor", "areas"},
			Item: &LayerItem{
				KeyField: "name",
				Key:      name,
				Fields:   map[string]string{"level": string(a.Level)},
				Items:    map[string][]LayerItem{"members": {member}},
			},
		})
		after.ProjectAreas = append(after.ProjectAreas, rigor.Area{
			Name:    name,
			Members: []rigor.Member{{Repo: repo, Paths: []string{a.Match}}},
			Level:   a.Level,
		})
		out.Lines = append(out.Lines, areaLine(repo, a, name))
		size.areas++
		wrote = true
	}
	out.Lines = append(out.Lines, levelChangeLines(repo, before, after, project.Areas, mine.areas)...)
	if wrote {
		out.Records = append(out.Records, slug)
	}
	return out
}

// areaLine is what became of one rule: its name and level as the file writes
// them, and the project area that replaces it.
func areaLine(repo string, a rigor.Area, name string) string {
	line := fmt.Sprintf("%s: rigor.areas %q %s -> defaults.rigor.areas %q (member %s, paths %s", repo, a.Name, a.Level, name, repo, a.Match)
	if name != a.Name {
		line += fmt.Sprintf("; the name %q is taken", a.Name)
	}
	return line + ")"
}

// areaName is the name a moved rule takes (R4): its own where the project
// layer is free of it, and otherwise the name with the repository appended —
// acme/app's `ui` is `ui-acme-app` — cut to what a name may be. A cut that
// collides too takes a short hash, because cutting alone would give two
// repositories with long names one area, and one area is one rule.
//
// taken is every name the project layer holds and every name this migration
// has already given out; the answer is added to it by the caller.
func areaName(base, owner string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	appended := base + "-" + owner
	if cut := cutName(appended, rigor.MaxAreaName); !taken[cut] {
		return cut
	}
	// The hash is of the repository, the rule's name and the attempt, so two
	// repositories that both need one get two. The loop is bounded by the
	// names in play and not by a figure of its own: every attempt is a
	// different input to the hash, so exhausting it would take two inputs
	// whose first 8 hex agree.
	last := ""
	for i := 0; i <= len(taken); i++ {
		h := shortHash(owner + "\x00" + base + "\x00" + strconv.Itoa(i))
		last = cutName(appended, rigor.MaxAreaName-1-len(h)) + "-" + h
		if !taken[last] {
			return last
		}
	}
	return last
}

// cutName is s at n bytes at most, without the hyphens a cut may leave at the
// end: a name ending in one is a name missing the rest of a word.
func cutName(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.TrimRight(s, "-")
}

// nameHashHex is the width of a name's hash suffix, half of a slug's: enough
// to keep two repositories with one readable half apart (D3302's copy name
// makes the same choice for the same reason).
const nameHashHex = 8

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:nameHashHex]
}

// ownerRepoOf is the readable half of a repository's slug, which R4 appends to
// an area name: A4's <owner-repo>, "acme/app" as "acme-app". It is cut from
// the slug rather than spelled again, so the two cannot drift; the hash half
// follows the last hyphen, and the readable half never ends with one
// (task.Slug trims them and replaces every run of others).
func ownerRepoOf(slug string) string {
	if i := strings.LastIndex(slug, "-"); i > 0 {
		return slug[:i]
	}
	return slug
}

// everyPath is the label R5's own probe prints under: the level the layers
// resolve for the paths of the repository that no rule of their own covers.
const everyPath = "every path"

// probePath matches no rule of its own in any realistic layer, which is what
// makes it the probe: it answers with the level of the fallback and not with
// one a rule set. A project-wide glob (`**`) matching it decides nothing —
// that rank is the same before and after the move.
const probePath = "fugaro-lift-probe"

// levelChangeLines names every path whose level moves under D3294's order,
// with the old and the new level (R5). It is the whole of R5: update prints
// these before it writes anything, so the person upgrading sees what the
// migration is about to do to each of their repositories.
//
// The paths compared are the ones that can decide differently, one per rule
// that can be in effect for this repository: a path inside each moved rule's
// match, a path inside each glob a project area names this repository at, and
// one probe for the paths no rule covers. Nothing else can move: a
// project-wide area is read at the same rank before and after, and every rule
// of the repository's own file is one of the moved ones.
func levelChangeLines(repo string, before, after rigor.Config, projectAreas, moved []rigor.Area) []string {
	var out []string
	for _, p := range probePaths(repo, projectAreas, moved) {
		was := rigor.Resolve(before, rigor.Request{Repo: repo, Paths: p.paths}).Level
		now := rigor.Resolve(after, rigor.Request{Repo: repo, Paths: p.paths}).Level
		if was != now {
			out = append(out, fmt.Sprintf("%s: %s %s -> %s", repo, p.label, was, now))
		}
	}
	return out
}

// samplePath is one path a doublestar glob covers. The glob's own text is one
// only while no alternation and no character class is in it: the matcher reads
// `ui/**` as the path "ui/**", but `{ui,mobile}/**` and `[abc]/**` are paths no
// file has, so a rule carrying one is asked about a path it does not cover and
// its level change goes unnamed (R5). The first alternative of each group and
// the first member of each class stand in for them, which expands to a path the
// glob really does cover.
func samplePath(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '\\':
			// An escape stands for the character after it, which is the
			// character the path spells.
			if i+1 < len(pattern) {
				i++
				b.WriteByte(pattern[i])
				continue
			}
			b.WriteByte(c)
		case '{':
			end := closingBrace(pattern, i)
			if end < 0 {
				b.WriteByte(c)
				continue
			}
			b.WriteString(samplePath(firstAlternative(pattern[i+1 : end])))
			i = end
		case '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				b.WriteByte(c)
				continue
			}
			end += i + 1
			member := classSample(pattern[i+1 : end])
			if member == "" {
				b.WriteByte(c)
				continue
			}
			b.WriteString(member)
			i = end
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// classCandidate is what a class's sample is picked from — the characters the
// patterns of a layer name — so a class that covers every one of them has no
// sample and the caller keeps the pattern's own text.
const classCandidate = "abcdefghijklmnopqrstuvwxyz0123456789_-.~"

// classSample is one character the character class body covers: its first
// member, and for a negated class (a leading ! or ^) the first candidate the
// class leaves out, which is the only kind of member a negated class has. A
// body naming nothing — the class an unclosed [ leaves, which doublestar's
// validation refuses before a rule reaches the layer — answers "".
func classSample(body string) string {
	if body == "" {
		return ""
	}
	negated := body[0] == '!' || body[0] == '^'
	if negated {
		body = body[1:]
		for i := 0; i < len(classCandidate); i++ {
			if !classNames(body, classCandidate[i]) {
				return string(classCandidate[i])
			}
		}
		return ""
	}
	return string(body[0])
}

// classNames reports whether the class body names c, ranges included.
func classNames(body string, c byte) bool {
	for i := 0; i < len(body); i++ {
		switch {
		case i+2 < len(body) && body[i+1] == '-':
			if body[i] <= c && c <= body[i+2] {
				return true
			}
			i += 2
		case body[i] == c:
			return true
		}
	}
	return false
}

// closingBrace is the index of the brace closing the group opened at i, or -1
// when the pattern has none: doublestar's own validation refuses such a glob
// before a rule reaches the layer, and a literal brace is the safe reading of
// one that got here anyway.
func closingBrace(pattern string, i int) int {
	depth := 0
	for j := i; j < len(pattern); j++ {
		switch pattern[j] {
		case '\\':
			j++
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// firstAlternative is a group's body up to its first comma at the group's own
// depth: the first of the paths the group spells, a nested group's commas
// belonging to it.
func firstAlternative(body string) string {
	depth := 0
	for j := 0; j < len(body); j++ {
		switch body[j] {
		case '\\':
			j++
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case ',':
			if depth == 0 {
				return body[:j]
			}
		}
	}
	return body
}

type probe struct {
	label string
	paths []string
}

// probePaths is the paths above, deduplicated by label: two rules with one
// match text decide the same level for it, so one comparison says it. Each is
// a sample of the glob and not the glob's own text, which the matcher reads as
// a path only when the glob carries no alternation and no character class.
func probePaths(repo string, projectAreas, moved []rigor.Area) []probe {
	seen := map[string]bool{}
	var out []probe
	add := func(label string, paths []string) {
		if seen[label] {
			return
		}
		seen[label] = true
		out = append(out, probe{label: label, paths: paths})
	}
	for _, a := range moved {
		add(a.Match, []string{samplePath(a.Match)})
	}
	for _, a := range projectAreas {
		for _, m := range a.Members {
			if m.Repo != repo {
				continue
			}
			for _, p := range m.Paths {
				add(p, []string{samplePath(p)})
			}
		}
	}
	// A member with no paths is the whole repository, and the probe is inside
	// it, so that case needs nothing of its own.
	add(everyPath, []string{probePath})
	return out
}

// stillWritten are the lines for a repository already migrated (D3305) whose
// file still writes one of this item's keys: C1's own sentence, which names
// the key, where the project states it now, and the command that writes it
// there. It is one line per key, and no write: update cannot commit to a
// repository, so deleting the key is the person's.
func stillWritten(repo string, data []byte) []string {
	var out []string
	for _, n := range config.LiftedKeyNotes(data) {
		if n.Path != "rigor.default" && n.Path != "rigor.areas" {
			continue
		}
		out = append(out, repo+": "+n.Why)
	}
	return out
}

// leftAlone is the line for a repository this item cannot move: what is wrong,
// and that nothing of it was touched, since a half-migrated block loses the
// rest of it when release A stops reading the key.
func leftAlone(repo string, err error) string {
	return fmt.Sprintf("%s: %v; nothing of its rigor: block was moved", repo, err)
}

// sortedRepos orders the repositories by name and provider, so the lines,
// the writes and the records do not move between two runs of update over the
// same project. The provider breaks a tie: one repository name is listed by
// two providers only in a project that admits both, and the two are two
// repositories to the migration record.
func sortedRepos(repos []Repo) []Repo {
	out := slices.Clone(repos)
	slices.SortFunc(out, func(a, b Repo) int {
		if c := strings.Compare(a.Repo, b.Repo); c != 0 {
			return c
		}
		return strings.Compare(a.Provider, b.Provider)
	})
	return out
}
