package config

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// dropLiftedKeys is tree with every lifted key a repository's file writes
// removed, and the note of each (config.LiftedKeys, R2 of
// docs/repo-override-rulings/reconciliation.md, D3237). It is the one drop:
// the merge never sees a lifted value, so no reader downstream — the merge,
// the repository's own flags or a config a run carries — has to know a key was
// lifted in order to stop honouring it.
//
// The answer is a copy, and tree itself is left as it was written: Resolve
// judges a file's problems on the file as written (spec §4), where the lifted
// keys still decode and still validate for one release, and merges the copy —
// so the tree it drops from is never the only copy of what the file said.
//
// A block left empty by the drop goes with its keys, so a file that wrote
// nothing but lifted keys resolves exactly as one that wrote none of them:
// `ab: {rate: 0}` is the repository's own A/B opt-out (ee/ab's abBlock reads
// Config.AB's presence, D556/D557), and a block left standing beside the
// project's would keep the opt-out the drop removed. `rigor:` alone stays when
// its keys go: the block's own presence is the repository opting in
// (RepoRigorSet, setsRigor) and R2 keeps it opting in after the drop — a file
// that wrote `rigor: {default: hotfix}` still runs by the repository's own
// rules, with no level of its own.
func dropLiftedKeys(tree map[string]any) (map[string]any, []RetiredKeyNote) {
	out := copyTree(tree)
	notes, _ := dropLifted(out, "")
	return out, notes
}

// copyTree is tree with every mapping under it copied and the leaves shared: a
// *yaml.Node is read and never written once it is in the tree, so the copy is
// a tree of its own to delete keys from (dropLifted) while the nodes the file
// wrote stay the ones both trees go on to marshal.
func copyTree(tree map[string]any) map[string]any {
	out := make(map[string]any, len(tree))
	for k, v := range tree {
		if sub, ok := v.(map[string]any); ok {
			v = copyTree(sub)
		}
		out[k] = v
	}
	return out
}

// dropLifted is dropLiftedKeys at one mapping, and says whether anything under
// it was dropped, which is what lets its caller take a block the drop emptied.
func dropLifted(m map[string]any, path string) ([]RetiredKeyNote, bool) {
	var notes []RetiredKeyNote
	dropped := false
	for _, k := range sortedKeys(m) {
		p := k
		if path != "" {
			p = path + "." + k
		}
		if row, ok := liftedRow(p); ok {
			delete(m, k)
			notes = append(notes, RetiredKeyNote{Path: p, Why: row.Why})
			dropped = true
			continue
		}
		sub, ok := m[k].(map[string]any)
		if !ok {
			continue
		}
		subNotes, subDropped := dropLifted(sub, p)
		notes = append(notes, subNotes...)
		dropped = dropped || subDropped
		if subDropped && len(sub) == 0 && p != "rigor" {
			delete(m, k)
		}
	}
	return notes, dropped
}

// decodeTree is decodeRepo's strict decode over the tree Resolve has dropped
// the lifted keys from instead of a file's own bytes: yaml.Marshal writes the
// tree's nodes back as the text and tag they were written with, so what comes
// out is the file minus the keys it may no longer decide. Both callers have
// read the same document with decodeRepo already, so a problem here is not
// expected and carries no line of either file — the layer arm's own decode
// says so in the same words.
func decodeTree(tree map[string]any) (*Config, []Problem) {
	out, err := yaml.Marshal(tree)
	if err != nil {
		return nil, []Problem{{Message: "resolving: " + err.Error()}}
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(out))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		ps := yamlProblems(err)
		for i := range ps {
			ps[i].Line, ps[i].Message = 0, "resolving: "+ps[i].Message
		}
		return nil, ps
	}
	return &c, nil
}
