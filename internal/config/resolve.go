package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Sources of a resolved value (Resolution.SourceOf).
const (
	SourceDefault = "default"
	SourceProject = "project"
	SourceRepo    = "repo"
)

// SourceProfile is the source of a value a profile set.
func SourceProfile(name string) string { return "profile " + name }

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
}

// SourceOf is the source of path: its own, else its nearest listed
// ancestor's, else SourceDefault.
func (r *Resolution) SourceOf(path string) string {
	for p := path; p != ""; {
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
// and Fugaro's defaults (docs/design/layered-config.md §4): for every key,
// the repository's value, else its workflow's profile's, else the project
// layer's defaults', else Fugaro's default. Maps merge key by key; a
// scalar or a list is replaced whole; null is "not set here". A nil l is
// Parse as before 0.6.0, except that naming a profile is an error.
func Resolve(data []byte, l *ProjectLayer) (*Config, *Resolution, []Problem) {
	repo, ps := decodeRepo(data)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	var tree map[string]any
	if err := yaml.Unmarshal(data, &tree); err != nil {
		return nil, nil, yamlProblems(err)
	}
	if l == nil {
		if ps := layerlessProblems(repo); len(ps) > 0 {
			return nil, nil, ps
		}
		applyDefaults(repo)
		if ps := Validate(repo); len(ps) > 0 {
			return nil, nil, noLayerHint(repo, ps)
		}
		res := &Resolution{Sources: map[string]string{}}
		markLeaves(tree, "", SourceRepo, res.Sources)
		res.ConfigSHA256 = repo.SHA256()
		return repo, res, nil
	}
	if ps := anchorProblems(repo, l); len(ps) > 0 {
		return nil, nil, ps
	}
	res := &Resolution{Sources: map[string]string{}, LayerSHA256: l.SHA256}
	merged := map[string]any{}
	if d, ok := l.tree["defaults"].(map[string]any); ok {
		overlay(merged, d, "", SourceProject, res.Sources)
	}
	top := maps1(tree)
	delete(top, "workflows")
	overlay(merged, top, "", SourceRepo, res.Sources)
	wfs, profileOf, ps := resolveWorkflows(tree, repo, l, res.Sources)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	merged["workflows"] = wfs
	out, err := yaml.Marshal(merged)
	if err != nil {
		return nil, nil, []Problem{{Message: "resolving: " + err.Error()}}
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(out))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, nil, yamlProblems(err)
	}
	c.Layer = l
	applyDefaults(&c)
	if ps := Validate(&c); len(ps) > 0 {
		return nil, nil, annotate(ps, res, profileOf)
	}
	res.ConfigSHA256 = c.SHA256()
	return &c, res, nil
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
			ps = append(ps, Problem{Path: "workflows." + name + ".profile", Message: fmt.Sprintf("names profile %q, but %s", p, why), Code: CodeNeedsLayer})
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
			ps[i].Message += fmt.Sprintf("; a fugaro.yaml without workflows takes one from project %s's layer, and none was found (fugaro config layer)", c.Project)
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
func resolveWorkflows(tree map[string]any, repo *Config, l *ProjectLayer, src map[string]string) (map[string]any, map[string]string, []Problem) {
	profiles, _ := l.tree["profiles"].(map[string]any)
	names := strings.Join(sortedKeys(profiles), ", ")
	profileTree := func(name string) (map[string]any, bool) {
		p, ok := profiles[name].(map[string]any)
		if !ok {
			return nil, false
		}
		q := deepCopy(p).(map[string]any)
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
		overlay(w, p, path, SourceProfile(name), src)
		w["profile"] = name
		src[path+".profile"] = from
		out[ImplicitWorkflow], profileOf[ImplicitWorkflow] = w, name
		return out, profileOf, nil
	}
	if repo.Profile != "" {
		return nil, nil, []Problem{{Path: "profile", Message: "applies only to a fugaro.yaml with no workflows:; name each workflow's profile with workflows.<name>.profile"}}
	}
	var ps []Problem
	for _, name := range sortedKeys(wm) {
		rw, _ := wm[name].(map[string]any)
		path := "workflows." + name
		w := map[string]any{}
		if pname := repo.Workflows[name].Profile; pname != "" {
			p, ok := profileTree(pname)
			if !ok {
				ps = append(ps, Problem{Path: path + ".profile", Message: fmt.Sprintf("names profile %q, which project %s's layer does not have (its profiles: %s)", pname, l.Project, names)})
				continue
			}
			// The inline escape hatch: a repository Dockerfile replaces the
			// profile's generated-image settings.
			if rw["dockerfile"] != nil {
				delete(p, "image")
			}
			overlay(w, p, path, SourceProfile(pname), src)
			profileOf[name] = pname
		}
		overlay(w, rw, path, SourceRepo, src)
		out[name] = w
	}
	return out, profileOf, ps
}

// overlay merges src into dst at path: maps key by key, anything else
// replaced whole; a null in src sets nothing. sources records the leaves
// src set.
func overlay(dst, src map[string]any, path string, source string, sources map[string]string) {
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
			dm, ok := dst[k].(map[string]any)
			if !ok {
				forget(sources, p)
				dm = map[string]any{}
				dst[k] = dm
			}
			overlay(dm, sm, p, source, sources)
			continue
		}
		forget(sources, p)
		dst[k] = deepCopy(v)
		sources[p] = source
	}
}

// forget drops the sources of path and everything under it.
func forget(sources map[string]string, path string) {
	for k := range sources {
		if k == path || strings.HasPrefix(k, path+".") {
			delete(sources, k)
		}
	}
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

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopy(e)
		}
		return out
	}
	return v
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
