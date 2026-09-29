package tf

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// sensitive names, per resource type, the attributes whose in-place update
// the summary lists first: they change who a job runs as or what it runs,
// or what gets deleted, or whether something billable starts on its own. An
// attribute matches when it is any segment of a changed path.
var sensitive = map[string][]string{
	"google_cloud_run_v2_job":             {"service_account", "image", "env", "max_retries"},
	"google_storage_bucket":               {"lifecycle_rule"},
	"google_artifact_registry_repository": {"cleanup_policies", "cleanup_policy_dry_run"},
	"google_cloud_scheduler_job":          {"paused"},
}

// singleBlocks names, per resource type, the nested blocks that hold at most
// one element. The plan JSON gives them as one-element lists; their [0] is
// left out of a path, so a job's image reads template.template.containers[0].image.
var singleBlocks = map[string][]string{
	"google_cloud_run_v2_job": {"template"},
}

// Summary describes a plan for the confirmation: the counts; then, marked
// with ⚠, the sensitive in-place updates and any delete or replace; then one
// line per other change, imports first. Updates carry their changed paths.
func Summary(p *Plan) string {
	if p == nil {
		return "No plan.\n"
	}
	var (
		nImport, nCreate, nUpdate, nForget, nReplace, nDelete int
		marked, imports, others                               []string
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
			marked = append(marked, "⚠ replace "+rc.Address)
			continue
		case slices.Equal(acts, []string{"delete"}):
			nDelete++
			marked = append(marked, "⚠ delete "+rc.Address)
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
			imports = append(imports, fmt.Sprintf("import %s (id %s)", rc.Address, rc.Change.Importing.ID))
			continue
		case slices.Equal(acts, []string{"no-op"}), slices.Equal(acts, []string{"read"}):
			continue
		default:
			marked = append(marked, fmt.Sprintf("⚠ %s %s", strings.Join(acts, "+"), rc.Address))
			continue
		}

		// An in-place update, maybe of an imported object.
		var paths []string
		diffPaths(rc.Change.Before, rc.Change.After, "", singleBlocks[rc.Type], &paths)
		verb := "update "
		if importing {
			verb = fmt.Sprintf("import (id %s) and update ", rc.Change.Importing.ID)
		}
		line := verb + rc.Address
		if len(paths) > 0 {
			line += ": " + strings.Join(paths, ", ")
		}
		switch {
		case isSensitive(rc.Type, paths):
			marked = append(marked, "⚠ "+line)
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
	if len(marked) > 0 {
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

func isSensitive(typ string, paths []string) bool {
	attrs := sensitive[typ]
	for _, p := range paths {
		for _, seg := range pathSegments(p) {
			if slices.Contains(attrs, seg) {
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
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			diffPaths(bm[k], am[k], p, single, out)
		}
		return
	}
	bl, bIsList := before.([]any)
	al, aIsList := after.([]any)
	if bIsList && aIsList {
		// A one-element block that is always one element: no index.
		name := prefix
		if i := strings.LastIndexByte(name, '.'); i >= 0 {
			name = name[i+1:]
		}
		if len(bl) == 1 && len(al) == 1 && slices.Contains(single, name) {
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
