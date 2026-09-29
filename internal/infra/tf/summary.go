package tf

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// sensitive names, per resource type, the attribute locations whose in-place
// update the summary lists first: they change who a job runs as or what it
// runs, or what gets deleted (a lifecycle, a cleanup policy, a log
// bucket's retention), or whether something billable starts on its
// own. A location is a dotted path without list indexes. A changed path
// matches when it lies under a location, or above one (a whole block that
// changes, or becomes unknown, holds the location).
var sensitive = map[string][]string{
	"google_cloud_run_v2_job": {
		"template.template.service_account",
		"template.template.containers.image",
		"template.template.containers.env",
		"template.template.max_retries",
	},
	"google_storage_bucket":               {"lifecycle_rule"},
	"google_artifact_registry_repository": {"cleanup_policies", "cleanup_policy_dry_run"},
	"google_cloud_scheduler_job":          {"paused"},
	// Lowering a log bucket's retention purges the older entries.
	"google_logging_project_bucket_config": {"retention_days"},
}

// singleBlocks names, per resource type, the nested blocks that hold at most
// one element. The plan JSON gives them as one-element lists; their [0] is
// left out of a path, so a job's image reads template.template.containers[0].image.
var singleBlocks = map[string][]string{
	"google_cloud_run_v2_job": {"template"},
}

// Summary describes a plan for the confirmation: the counts; then, marked
// with ⚠, the sensitive in-place updates, followed by any delete or replace;
// then one line per other change, imports first. Updates carry their changed
// paths.
func Summary(p *Plan) string {
	if p == nil {
		return "No plan.\n"
	}
	var (
		nImport, nCreate, nUpdate, nForget, nReplace, nDelete int
		sensitiveUpdates, destructive, imports, others        []string
	)
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		importing := rc.Change.Importing != nil
		replace := slices.Contains(acts, "delete") && slices.Contains(acts, "create")
		if importing {
			nImport++
		}
		switch {
		case replace:
			nReplace++
			destructive = append(destructive, "⚠ replace "+rc.Address)
			continue
		case slices.Equal(acts, []string{"delete"}):
			nDelete++
			destructive = append(destructive, "⚠ delete "+rc.Address)
			continue
		case slices.Equal(acts, []string{"create"}):
			nCreate++
			others = append(others, "create "+rc.Address)
			continue
		case slices.Equal(acts, []string{"forget"}):
			nForget++
			others = append(others, "forget "+rc.Address)
			continue
		case slices.Equal(acts, []string{"update"}):
			nUpdate++
		case importing && slices.Equal(acts, []string{"no-op"}):
			imports = append(imports, "import "+rc.Address+importID(rc.Change.Importing))
			continue
		case slices.Equal(acts, []string{"no-op"}), slices.Equal(acts, []string{"read"}):
			continue
		default:
			destructive = append(destructive, fmt.Sprintf("⚠ %s %s", strings.Join(acts, "+"), rc.Address))
			continue
		}

		// An in-place update, maybe of an imported object.
		paths := changedPaths(rc.Type, rc.Change)
		line := "update " + rc.Address
		if importing {
			line = "import and update " + rc.Address + importID(rc.Change.Importing)
		}
		if len(paths) > 0 {
			line += ": " + strings.Join(paths, ", ")
		}
		switch {
		case isSensitive(rc.Type, paths):
			sensitiveUpdates = append(sensitiveUpdates, "⚠ "+line)
		case importing:
			imports = append(imports, line)
		default:
			others = append(others, line)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Plan: %d to import, %d to create, %d to update, %d to forget", nImport, nCreate, nUpdate, nForget)
	if nReplace > 0 {
		fmt.Fprintf(&b, ", %d to replace", nReplace)
	}
	if nDelete > 0 {
		fmt.Fprintf(&b, ", %d to delete", nDelete)
	}
	b.WriteString(".\n")
	if marked := append(sensitiveUpdates, destructive...); len(marked) > 0 {
		b.WriteString("\n⚠ Review these first:\n")
		for _, l := range marked {
			b.WriteString("  " + l + "\n")
		}
	}
	if rest := append(imports, others...); len(rest) > 0 {
		b.WriteString("\nOther changes:\n")
		for _, l := range rest {
			b.WriteString("  " + l + "\n")
		}
	}
	return b.String()
}

func importID(im *Importing) string {
	if im == nil || im.ID == "" {
		return "" // an import by identity has no ID
	}
	return " (id " + im.ID + ")"
}

// changedPaths returns the paths an update changes: where before and after
// differ, plus where after_unknown says a value is known only after apply
// (a value going from null to unknown is absent from both before and after).
func changedPaths(typ string, c Change) []string {
	var paths []string
	single := singleBlocks[typ]
	diffPaths(c.Before, c.After, "", single, &paths)
	var unknown []string
	unknownPaths(c.AfterUnknown, "", single, &unknown)
	for _, u := range unknown {
		if !slices.Contains(paths, u) {
			paths = append(paths, u)
		}
	}
	slices.Sort(paths)
	return paths
}

func isSensitive(typ string, paths []string) bool {
	for _, p := range paths {
		segs := pathSegments(p)
		for _, loc := range sensitive[typ] {
			l := strings.Split(loc, ".")
			n := min(len(segs), len(l))
			if slices.Equal(segs[:n], l[:n]) {
				return true
			}
		}
	}
	return false
}

// pathSegments returns the attribute names of a path, without list indexes.
func pathSegments(p string) []string {
	var segs []string
	for _, s := range strings.Split(p, ".") {
		if i := strings.IndexByte(s, '['); i >= 0 {
			s = s[:i]
		}
		segs = append(segs, s)
	}
	return segs
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// collapse reports whether the list at prefix is a one-element block whose
// [0] is left out of paths.
func collapse(prefix string, single []string, lens ...int) bool {
	for _, n := range lens {
		if n != 1 {
			return false
		}
	}
	name := prefix
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	return slices.Contains(single, name)
}

// diffPaths appends to out the paths where before and after differ. A value
// that becomes unknown until apply is absent from after, so it shows too.
func diffPaths(before, after any, prefix string, single []string, out *[]string) {
	if reflect.DeepEqual(before, after) {
		return
	}
	bm, bIsMap := before.(map[string]any)
	am, aIsMap := after.(map[string]any)
	if bIsMap && aIsMap {
		keys := make([]string, 0, len(bm)+len(am))
		for k := range bm {
			keys = append(keys, k)
		}
		for k := range am {
			if _, ok := bm[k]; !ok {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		for _, k := range keys {
			diffPaths(bm[k], am[k], join(prefix, k), single, out)
		}
		return
	}
	bl, bIsList := before.([]any)
	al, aIsList := after.([]any)
	if bIsList && aIsList {
		if collapse(prefix, single, len(bl), len(al)) {
			diffPaths(bl[0], al[0], prefix, single, out)
			return
		}
		for i := range max(len(bl), len(al)) {
			var b, a any
			if i < len(bl) {
				b = bl[i]
			}
			if i < len(al) {
				a = al[i]
			}
			diffPaths(b, a, prefix+"["+strconv.Itoa(i)+"]", single, out)
		}
		return
	}
	if prefix == "" {
		prefix = "(all)"
	}
	*out = append(*out, prefix)
}

// unknownPaths appends to out the paths that after_unknown marks true.
func unknownPaths(u any, prefix string, single []string, out *[]string) {
	switch v := u.(type) {
	case bool:
		if v {
			if prefix == "" {
				prefix = "(all)"
			}
			*out = append(*out, prefix)
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			unknownPaths(v[k], join(prefix, k), single, out)
		}
	case []any:
		if collapse(prefix, single, len(v)) {
			unknownPaths(v[0], prefix, single, out)
			return
		}
		for i, e := range v {
			unknownPaths(e, prefix+"["+strconv.Itoa(i)+"]", single, out)
		}
	}
}
